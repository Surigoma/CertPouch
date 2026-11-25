package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/dns"
)

const managedNSDHeader = "# Managed by CertPouch\n"

func prepareNSD(ctx context.Context, deployment config.DNSDeployment, candidate string, control nsdCommands) error {
	include := filepath.Clean(deployment.NSDInclude)
	if !filepath.IsAbs(include) || include == filepath.Clean(deployment.NSDConfig) {
		return fmt.Errorf("NSD include_file must be an absolute dedicated include path")
	}
	checkConfig, err := exec.LookPath("nsd-checkconf")
	if err != nil {
		return err
	}
	_, err = readManagedNSDInclude(include)
	if err != nil {
		return err
	}
	reconfigure := func(ctx context.Context, names []string) error {
		var body strings.Builder
		body.WriteString(managedNSDHeader)
		for _, name := range names {
			path := filepath.Join(deployment.ZoneDir, "current", name+".zone")
			if strings.ContainsAny(path, "\"\\\r\n") {
				return fmt.Errorf("unsupported NSD zone file path")
			}
			fmt.Fprintf(&body, "zone:\n  name: \"%s\"\n  zonefile: \"%s\"\n", name, path)
		}
		if err := replaceNSDInclude(include, []byte(body.String())); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, checkConfig, deployment.NSDConfig).Run(); err != nil {
			return fmt.Errorf("validate NSD configuration: %w", err)
		}
		for _, name := range names {
			want := filepath.Join(deployment.ZoneDir, "current", name+".zone")
			got, err := exec.CommandContext(ctx, checkConfig, "-o", "zonefile", "-z", name, deployment.NSDConfig).Output()
			if err != nil || strings.TrimSpace(string(got)) != want {
				return fmt.Errorf("NSD does not reference the managed path for %s", name)
			}
		}
		return exec.CommandContext(ctx, control.controller, "-c", control.config, "reconfig").Run()
	}
	err = dns.PrepareZones(ctx, deployment.ZoneDir, candidate, control, reconfigure)
	if err != nil {
		return fmt.Errorf("prepare NSD zones using %s: %w", include, err)
	}
	return nil
}

func readManagedNSDInclude(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("NSD include must be a regular file protected from other writers")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(content) > 1<<20 || !bytes.HasPrefix(content, []byte(managedNSDHeader)) {
		return nil, fmt.Errorf("NSD include must begin with %q", strings.TrimSpace(managedNSDHeader))
	}
	return content, nil
}

func replaceNSDInclude(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".certpouch-nsd-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
