package dns

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strings"
)

// ReadLegacyCache reads the Google Sheets batchGet snapshot written by zone_gene.py.
// The fallback prefix and zone are supplied by the operator.
func ReadLegacyCache(r io.Reader, fallback, zone string) (Inventory, error) {
	var response struct {
		Ranges []struct {
			Values [][]string `json:"values"`
		} `json:"valueRanges"`
	}
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&response); err != nil || len(response.Ranges) == 0 {
		return Inventory{}, fmt.Errorf("invalid or empty Sheets snapshot")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Inventory{}, fmt.Errorf("unexpected data after Sheets snapshot")
	}
	groups := make([][][]string, 0, len(response.Ranges))
	for _, group := range response.Ranges {
		groups = append(groups, group.Values)
	}
	return legacyRows(groups, fallback, zone)
}

// ReadLegacyCSV reads one Google Sheets CSV export per source, with CSV quoting
// honored for cells that contain comma-separated DNS aliases.
func ReadLegacyCSV(sources []io.Reader, fallback, zone string) (Inventory, error) {
	groups := make([][][]string, 0, len(sources))
	for i, source := range sources {
		reader := csv.NewReader(source)
		reader.FieldsPerRecord = -1
		rows, err := reader.ReadAll()
		if err != nil {
			return Inventory{}, fmt.Errorf("invalid DNS CSV source %d: %w", i+1, err)
		}
		if len(rows) != 0 && len(rows[0]) != 0 {
			rows[0][0] = strings.TrimPrefix(rows[0][0], "\ufeff")
		}
		groups = append(groups, rows)
	}
	return legacyRows(groups, fallback, zone)
}

func legacyRows(groups [][][]string, fallback, zone string) (Inventory, error) {
	if _, ok := hostname("host." + zone); !ok || strings.TrimSpace(zone) == "" {
		return Inventory{}, fmt.Errorf("invalid legacy inventory zone")
	}
	fallback = strings.TrimSuffix(fallback, ".")
	if _, err := netip.ParseAddr(fallback + ".0"); err != nil {
		return Inventory{}, fmt.Errorf("invalid legacy inventory fallback prefix")
	}
	var records []Record
	for rangeIndex, rows := range groups {
		prefix := fallback
		for rowIndex, row := range rows {
			if len(row) < 3 || row[0] != "" || row[2] == "" {
				continue
			}
			if row[1] != "" {
				prefix = strings.TrimSuffix(row[1], ".")
			}
			if len(row) < 4 || row[3] == "" {
				continue
			}
			value := prefix + "." + row[2]
			addr, err := netip.ParseAddr(value)
			if err != nil || !addr.Is4() {
				return Inventory{}, fmt.Errorf("invalid IPv4 at range %d, row %d", rangeIndex+1, rowIndex+1)
			}
			records = append(records, Record{Name: row[3] + ".pvt." + zone, Type: "A", Value: value})
			if len(row) > 6 {
				for alias := range strings.SplitSeq(row[6], ",") {
					alias = strings.ReplaceAll(alias, " ", "")
					if alias != "" {
						records = append(records, Record{Name: alias + "." + zone, Type: "A", Value: value})
					}
				}
			}
		}
	}
	return NewInventory(records)
}
