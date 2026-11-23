package dns

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strings"
)

// ReadLegacyCache reads the Google Sheets batchGet snapshot written by zone_gene.py.
// The fallback prefix and zone are supplied by the operator.
func ReadLegacyCache(r io.Reader, fallback, zone string) (Inventory, error) {
	if _, ok := hostname("host." + zone); !ok || strings.TrimSpace(zone) == "" {
		return Inventory{}, fmt.Errorf("invalid legacy inventory zone")
	}
	fallback = strings.TrimSuffix(fallback, ".")
	if _, err := netip.ParseAddr(fallback + ".0"); err != nil {
		return Inventory{}, fmt.Errorf("invalid legacy inventory fallback prefix")
	}
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
	var records []Record
	for rangeIndex, group := range response.Ranges {
		prefix := fallback
		for rowIndex, row := range group.Values {
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
