package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Surigoma/CertPouch/internal/config"
)

type repeatedFlag []string

func (values *repeatedFlag) String() string { return strings.Join(*values, ",") }
func (values *repeatedFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("value must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func runConfigure(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: certpouch configure dns --help\n       certpouch configure server|client --help")
		return 2
	}
	if args[0] == "server" || args[0] == "client" {
		return runInit(args, stdout, stderr)
	}
	if args[0] != "dns" {
		fmt.Fprintln(stderr, "Usage: certpouch configure dns --help")
		return 2
	}
	flags := flag.NewFlagSet("configure dns", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", "", "absolute path to a new configuration directory")
	interactive := flags.Bool("interactive", false, "prompt for values not provided as flags")
	cache := flags.String("cache", "", "absolute path to the DNS inventory cache")
	zone := flags.String("zone", "", "forward DNS zone")
	prefix := flags.String("fallback-prefix", "", "IPv4 prefix used before inventory overrides")
	primary := flags.String("primary", "", "SOA primary nameserver")
	mailbox := flags.String("mailbox", "", "SOA responsible mailbox in DNS name form")
	serial := flags.Uint64("serial", 1, "initial SOA serial")
	ttl := flags.Uint64("ttl", 300, "zone TTL")
	spreadsheet := flags.String("spreadsheet-id", "", "optional Google Sheets spreadsheet ID")
	serviceAccount := flags.String("service-account", "", "absolute path to the service account JSON")
	var nameservers, ranges repeatedFlag
	flags.Var(&nameservers, "nameserver", "authoritative nameserver; repeat for multiple values")
	flags.Var(&ranges, "range", "Google Sheets range; repeat for multiple values")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if *interactive {
		reader := bufio.NewReader(stdin)
		var err error
		for _, item := range []struct {
			label string
			value *string
		}{
			{"Output directory", out}, {"Inventory cache", cache}, {"DNS zone", zone},
			{"Fallback IPv4 prefix", prefix}, {"SOA primary", primary},
		} {
			label, value := item.label, item.value
			if *value == "" {
				*value, err = prompt(reader, stdout, label, "")
				if err != nil {
					fmt.Fprintln(stderr, err)
					return 1
				}
			}
		}
		if *mailbox == "" {
			*mailbox, err = prompt(reader, stdout, "SOA mailbox", "hostmaster."+strings.TrimSuffix(*zone, "."))
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		if len(nameservers) == 0 {
			value, err := prompt(reader, stdout, "Nameserver", *primary)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			nameservers = append(nameservers, value)
		}
		if *spreadsheet == "" && *serviceAccount == "" && len(ranges) == 0 {
			answer, err := prompt(reader, stdout, "Configure Google Sheets fetching? (y/N)", "n")
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
				*spreadsheet = "prompt"
			}
		}
		if *spreadsheet != "" || *serviceAccount != "" || len(ranges) != 0 {
			for _, item := range []struct {
				label string
				value *string
			}{
				{"Spreadsheet ID", spreadsheet}, {"Service account JSON", serviceAccount},
			} {
				label, value := item.label, item.value
				if *value == "" || *value == "prompt" {
					*value, err = prompt(reader, stdout, label, "")
					if err != nil {
						fmt.Fprintln(stderr, err)
						return 1
					}
				}
			}
			if len(ranges) == 0 {
				value, err := prompt(reader, stdout, "Sheet range", "")
				if err != nil {
					fmt.Fprintln(stderr, err)
					return 1
				}
				ranges = append(ranges, value)
			}
		}
	}
	if *mailbox == "" && *zone != "" {
		*mailbox = "hostmaster." + strings.TrimSuffix(*zone, ".")
	}
	if len(nameservers) == 0 && *primary != "" {
		nameservers = append(nameservers, *primary)
	}
	if !filepath.IsAbs(*out) || !filepath.IsAbs(*cache) || *zone == "" || *prefix == "" || *primary == "" ||
		*mailbox == "" || len(nameservers) == 0 || *serial > 1<<32-1 || *ttl > 1<<32-1 {
		flags.Usage()
		return 2
	}
	sheetsConfigured := *spreadsheet != "" || *serviceAccount != "" || len(ranges) != 0
	if sheetsConfigured && (*spreadsheet == "" || !filepath.IsAbs(*serviceAccount) || len(ranges) == 0) {
		fmt.Fprintln(stderr, "Google Sheets configuration requires --spreadsheet-id, --service-account, and at least one --range")
		return 2
	}
	path, err := writeDNSConfiguration(filepath.Clean(*out), dnsConfigInput{
		Cache: *cache, Zone: *zone, Prefix: *prefix, Primary: *primary, Mailbox: *mailbox,
		Nameservers: nameservers, Serial: uint32(*serial), TTL: uint32(*ttl),
		Spreadsheet: *spreadsheet, ServiceAccount: *serviceAccount, Ranges: ranges,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

func prompt(reader *bufio.Reader, output io.Writer, label, defaultValue string) (string, error) {
	if defaultValue == "" {
		fmt.Fprintf(output, "%s: ", label)
	} else {
		fmt.Fprintf(output, "%s [%s]: ", label, defaultValue)
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		value = defaultValue
	}
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", label)
	}
	return value, nil
}

type dnsConfigInput struct {
	Cache, Zone, Prefix, Primary, Mailbox, Spreadsheet, ServiceAccount string
	Nameservers, Ranges                                                []string
	Serial, TTL                                                        uint32
}

func writeDNSConfiguration(dir string, input dnsConfigInput) (path string, err error) {
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", fmt.Errorf("create new configuration directory: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}()
	quotedList := func(values []string) string {
		quoted := make([]string, len(values))
		for i, value := range values {
			quoted[i] = strconv.Quote(value)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	dnsBody := fmt.Sprintf(`inventory:
  cache_file: %q
  fallback_prefix: %q
`, input.Cache, input.Prefix)
	if input.Spreadsheet != "" {
		dnsBody += fmt.Sprintf(`  sheets:
    spreadsheet_id: %q
    ranges: %s
    credentials_file: %q
`, input.Spreadsheet, quotedList(input.Ranges), input.ServiceAccount)
	}
	dnsBody += fmt.Sprintf(`dns:
  zone: %q
  zones:
    primary: %q
    mailbox: %q
    nameservers: %s
    ttl: %d
    serial: %d
    refresh: 3600
    retry: 600
    expire: 604800
    negative_ttl: 300
`, input.Zone, input.Primary, input.Mailbox, quotedList(input.Nameservers), input.TTL, input.Serial)
	path = filepath.Join(dir, "server.yaml")
	if err := os.WriteFile(path, []byte(dnsBody), 0600); err != nil {
		return "", err
	}
	if err := config.ValidateDNSConfig(path); err != nil {
		return "", fmt.Errorf("validate generated DNS config: %w", err)
	}
	if input.Spreadsheet != "" {
		if _, err := config.LoadSheetsFetch(path); err != nil {
			return "", fmt.Errorf("validate generated Sheets config: %w", err)
		}
	}
	return path, nil
}
