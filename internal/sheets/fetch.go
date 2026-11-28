// Package sheets reads DNS inventory ranges using the Google Sheets Go client.
package sheets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	sheetsapi "google.golang.org/api/sheets/v4"
)

const (
	tokenEndpoint    = "https://oauth2.googleapis.com/token"
	sheetsEndpoint   = "https://sheets.googleapis.com/"
	MaxSnapshotBytes = 16 << 20
)

var (
	spreadsheetPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	rangePattern       = regexp.MustCompile(`^A2:G([0-9]{1,7})$`)
)

type Credentials struct {
	config *jwt.Config
}

func ParseCredentials(raw []byte) (Credentials, error) {
	config, err := google.JWTConfigFromJSON(raw, sheetsapi.SpreadsheetsReadonlyScope)
	if err != nil || !strings.HasSuffix(config.Email, ".gserviceaccount.com") ||
		!strings.Contains(config.Email, "@") || strings.ContainsAny(config.Email, " \t\r\n") ||
		config.TokenURL != tokenEndpoint || len(config.PrivateKey) == 0 {
		return Credentials{}, fmt.Errorf("invalid Google service account credentials")
	}
	return Credentials{config}, nil
}

type Client struct {
	transport           http.RoundTripper
	tokenURL, sheetsURL string
}

func NewClient() *Client {
	return newClient(http.DefaultTransport, tokenEndpoint, sheetsEndpoint)
}

func newClient(transport http.RoundTripper, tokenURL, sheetsURL string) *Client {
	return &Client{transport: transport, tokenURL: tokenURL, sheetsURL: strings.TrimRight(sheetsURL, "/") + "/"}
}

func (c *Client) Fetch(ctx context.Context, credentials Credentials, spreadsheet string, ranges []string) ([]byte, error) {
	if credentials.config == nil {
		return nil, fmt.Errorf("invalid service account")
	}
	requested, err := validateRequest(spreadsheet, ranges)
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, credentials, spreadsheet, ranges, requested)
}

// ValidateRequest checks identifiers and ranges without performing network I/O.
func ValidateRequest(spreadsheet string, ranges []string) error {
	_, err := validateRequest(spreadsheet, ranges)
	return err
}

func validateRequest(spreadsheet string, ranges []string) ([]sheetRange, error) {
	if !spreadsheetPattern.MatchString(spreadsheet) || len(ranges) == 0 || len(ranges) > 100 {
		return nil, fmt.Errorf("invalid spreadsheet ID or number of ranges")
	}
	requested := make([]sheetRange, 0, len(ranges))
	seen := make(map[sheetRange]bool)
	for i, input := range ranges {
		region, ok := parseRange(input)
		if !ok || seen[region] {
			return nil, fmt.Errorf("invalid or duplicate Sheets range at index %d (expected Sheet!A2:Gn)", i)
		}
		seen[region] = true
		requested = append(requested, region)
	}
	return requested, nil
}

func (c *Client) fetch(ctx context.Context, credentials Credentials, spreadsheet string, ranges []string, requested []sheetRange) ([]byte, error) {
	base := &http.Client{
		Transport:     boundedTransport{base: c.transport, tokenURL: c.tokenURL},
		Timeout:       30 * time.Second,
		CheckRedirect: rejectRedirect,
	}
	jwtConfig := *credentials.config
	jwtConfig.TokenURL = c.tokenURL
	httpClient := jwtConfig.Client(context.WithValue(ctx, oauth2.HTTPClient, base))
	httpClient.CheckRedirect = rejectRedirect
	httpClient.Timeout = base.Timeout
	service, err := sheetsapi.NewService(ctx, option.WithHTTPClient(httpClient), option.WithEndpoint(c.sheetsURL))
	if err != nil {
		return nil, fmt.Errorf("initialize Google Sheets client")
	}
	result, err := service.Spreadsheets.Values.BatchGet(spreadsheet).Ranges(ranges...).
		MajorDimension("ROWS").ValueRenderOption("FORMATTED_VALUE").Context(ctx).Do()
	if err != nil {
		// Library errors can include response bodies, credentials, or range names.
		var apiErr *googleapi.Error
		var tokenErr *oauth2.RetrieveError
		switch {
		case ctx.Err() != nil:
			return nil, fmt.Errorf("Sheets batchGet: %w", ctx.Err())
		case errors.As(err, &apiErr):
			return nil, fmt.Errorf("Sheets batchGet returned HTTP %d", apiErr.Code)
		case errors.As(err, &tokenErr) && tokenErr.Response != nil:
			return nil, fmt.Errorf("Google OAuth token exchange returned HTTP %d", tokenErr.Response.StatusCode)
		default:
			return nil, fmt.Errorf("Google Sheets authentication or HTTPS fetch failed")
		}
	}
	return validateSnapshot(result, spreadsheet, requested)
}

func rejectRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

type boundedTransport struct {
	base     http.RoundTripper
	tokenURL string
}

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The generated client uses status codes; error bodies are not needed.
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return resp, nil
	}
	limit := int64(MaxSnapshotBytes)
	if req.URL.String() == t.tokenURL {
		limit = 64 << 10
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read Google API response")
	}
	if int64(len(raw)) > limit || !json.Valid(raw) {
		return nil, fmt.Errorf("Google API response is invalid or exceeds the size limit")
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

func validateSnapshot(result *sheetsapi.BatchGetValuesResponse, spreadsheet string, requested []sheetRange) ([]byte, error) {
	if result.SpreadsheetId != spreadsheet || len(result.ValueRanges) != len(requested) {
		return nil, fmt.Errorf("Sheets returned an incomplete or invalid snapshot")
	}
	for i, values := range result.ValueRanges {
		if values == nil {
			return nil, fmt.Errorf("Sheets returned an empty range at index %d", i)
		}
		actual, ok := parseRange(values.Range)
		rowDimension := values.MajorDimension == "ROWS" || (values.MajorDimension == "" && len(values.Values) == 0)
		if !ok || actual != requested[i] || !rowDimension || len(values.Values) >= actual.end {
			return nil, fmt.Errorf("Sheets returned an unexpected range at index %d", i)
		}
		for _, row := range values.Values {
			if len(row) > 7 {
				return nil, fmt.Errorf("Sheets returned unexpected columns at range index %d", i)
			}
			for _, cell := range row {
				if _, ok := cell.(string); !ok {
					return nil, fmt.Errorf("Sheets returned a non-text formatted cell at range index %d", i)
				}
			}
		}
	}
	snapshot, err := json.Marshal(result)
	if err != nil || len(snapshot) > MaxSnapshotBytes {
		return nil, fmt.Errorf("could not encode bounded Sheets snapshot")
	}
	return snapshot, nil
}

type sheetRange struct {
	sheet string
	end   int
}

func parseRange(input string) (sheetRange, bool) {
	split := strings.LastIndexByte(input, '!')
	if split <= 0 || len(input) > 512 || strings.ContainsAny(input, "\r\n\x00") {
		return sheetRange{}, false
	}
	sheet, cells := input[:split], input[split+1:]
	if strings.HasPrefix(sheet, "'") && strings.HasSuffix(sheet, "'") && len(sheet) >= 2 {
		sheet = strings.ReplaceAll(sheet[1:len(sheet)-1], "''", "'")
	} else if strings.ContainsAny(sheet, "'!") || strings.TrimSpace(sheet) != sheet {
		return sheetRange{}, false
	}
	match := rangePattern.FindStringSubmatch(cells)
	if sheet == "" || match == nil {
		return sheetRange{}, false
	}
	end, err := strconv.Atoi(match[1])
	return sheetRange{sheet, end}, err == nil && end >= 2
}
