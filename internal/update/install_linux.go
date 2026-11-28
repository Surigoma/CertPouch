package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Apply updates the standalone executable running this command.
func Apply(ctx context.Context, current string) (string, bool, error) {
	path, err := os.Executable()
	if err != nil {
		return "", false, err
	}
	running, err := os.Stat("/proc/self/exe")
	if err != nil {
		return "", false, err
	}
	return githubClient().apply(ctx, path, running, current)
}

func (g github) apply(ctx context.Context, path string, running os.FileInfo, current string) (string, bool, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", false, fmt.Errorf("no CertPouch release for linux/%s", runtime.GOARCH)
	}
	tag, newer, err := g.latest(ctx, current)
	if err != nil || !newer {
		return tag, false, err
	}
	dir := filepath.Dir(path)
	parent, err := os.Lstat(dir)
	if err != nil {
		return "", false, err
	}
	if !parent.IsDir() || !ownedAndProtected(parent) {
		return "", false, fmt.Errorf("the executable directory must be owned by the update user and protected from other writers")
	}
	if filepath.Base(dir) == "bin" {
		manifest := filepath.Join(filepath.Dir(dir), "share", "certpouch", "install-manifest")
		if _, err := os.Lstat(manifest); !os.IsNotExist(err) {
			return "", false, fmt.Errorf("use the release installer to update a managed installation")
		}
	}
	lock, err := lockUpdate(path + ".update-lock")
	if err != nil {
		return "", false, err
	}
	defer lock.Close()
	info, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || !ownedAndProtected(info) || !os.SameFile(running, info) {
		return "", false, fmt.Errorf("the executable is unsafe or was already replaced")
	}
	staging, err := os.MkdirTemp(dir, ".certpouch-update-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(staging)
	archive, err := os.Create(filepath.Join(staging, "release.tar.gz"))
	if err != nil {
		return "", false, err
	}
	defer archive.Close()
	name := "certpouch-linux-" + runtime.GOARCH
	if err := g.archive(ctx, tag, name+".tar.gz", archive); err != nil {
		return "", false, err
	}
	candidate := filepath.Join(staging, "certpouch")
	if err := extractExecutable(archive, name+"/certpouch", candidate, info.Mode().Perm()); err != nil {
		return "", false, err
	}
	if err := verifyExecutable(ctx, candidate, tag); err != nil {
		return "", false, err
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := replaceExecutable(path, info, candidate, staging); err != nil {
		return "", false, err
	}
	return tag, true, nil
}

func ownedAndProtected(info os.FileInfo) bool {
	return info.Sys().(*syscall.Stat_t).Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0022 == 0
}

func lockUpdate(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || !ownedAndProtected(info)) {
		err = fmt.Errorf("invalid update lock")
	}
	if err == nil {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("acquire update lock: %w", err)
	}
	return file, nil
}

func extractExecutable(archive io.Reader, member, destination string, mode os.FileMode) error {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer gz.Close()
	expanded := &io.LimitedReader{R: gz, N: 256 << 20}
	tarball := tar.NewReader(expanded)
	found := false
	for {
		entry, err := tarball.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if entry.Name != member {
			continue
		}
		if found || entry.Typeflag != tar.TypeReg || entry.Size <= 0 || entry.Size > maxArchiveBytes {
			return fmt.Errorf("invalid executable member in release archive")
		}
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = io.CopyN(file, tarball, entry.Size)
		if err == nil {
			err = file.Chmod(mode)
		}
		if err == nil {
			err = file.Sync()
		}
		if err := errors.Join(err, file.Close()); err != nil {
			return err
		}
		found = true
	}
	if !found || expanded.N == 0 {
		return fmt.Errorf("release archive has no bounded executable member")
	}
	return nil
}

func verifyExecutable(ctx context.Context, path, tag string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Path != "github.com/"+repository+"/cmd/certpouch" {
		return fmt.Errorf("release does not contain a CertPouch executable")
	}
	var goos, goarch string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "GOOS":
			goos = setting.Value
		case "GOARCH":
			goarch = setting.Value
		}
	}
	if goos != runtime.GOOS || goarch != runtime.GOARCH {
		return fmt.Errorf("release executable does not match this platform")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	command.Env = []string{}
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != tag {
		return fmt.Errorf("release executable did not report the expected version")
	}
	return nil
}

func replaceExecutable(path string, original os.FileInfo, candidate, staging string) error {
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(original, current) || !ownedAndProtected(current) ||
		original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()) {
		return fmt.Errorf("local executable changed during the update")
	}
	previous := path + ".previous"
	info, err := os.Lstat(previous)
	if (err != nil && !os.IsNotExist(err)) || (err == nil && (!info.Mode().IsRegular() || !ownedAndProtected(info))) {
		return fmt.Errorf("previous executable path is unsafe")
	}
	backup := filepath.Join(staging, "previous")
	if err := os.Link(path, backup); err != nil {
		return fmt.Errorf("preserve previous executable: %w", err)
	}
	if err := os.Rename(backup, previous); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return err
	}
	if err := os.Rename(candidate, path); err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("executable switched but directory sync failed: %w", err)
	}
	return nil
}
