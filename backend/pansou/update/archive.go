package update

import (
	"archive/tar"
	"compress/gzip"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func allowedPath(name string, dir bool) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	clean := path.Clean(name)
	if clean != strings.TrimSuffix(name, "/") || clean == "." || strings.HasPrefix(clean, "../") {
		return false
	}
	if dir {
		return clean == "bin" || clean == "web" || clean == "auth" || strings.HasPrefix(clean, "web/")
	}
	return clean == "bin/seekbox" || clean == "bin/gateway.py" || clean == "auth/login.cjs" || clean == "auth/cookies.cjs" || clean == "runtime.json" || strings.HasPrefix(clean, "web/")
}

func extractArchive(archive, destination string, m Manifest) (err error) {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	// Count all expanded bytes, including tar headers/padding and data after its end marker.
	limited := &io.LimitedReader{R: gz, N: MaxExpandedSize + 1}
	tr := tar.NewReader(limited)
	seen := map[string]bool{}
	var count int
	var totalSize int64
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		count++
		if count > 20000 {
			return errors.New("too many package entries")
		}
		dir := h.Typeflag == tar.TypeDir
		if !dir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return errors.New("package links and special files are forbidden")
		}
		if !allowedPath(h.Name, dir) || h.Linkname != "" {
			return fmt.Errorf("forbidden package path: %q", h.Name)
		}
		name := strings.TrimSuffix(h.Name, "/")
		if seen[name] {
			return errors.New("duplicate package entry")
		}
		seen[name] = true
		target := filepath.Join(destination, filepath.FromSlash(name))
		if dir {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if h.Size < 0 || h.Size > MaxExpandedSize-totalSize {
			return errors.New("invalid expanded file size")
		}
		totalSize += h.Size
		for key := range h.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse.") {
				return errors.New("sparse package files are forbidden")
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if name == "bin/seekbox" {
			mode = 0755
		}
		out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if e != nil {
			return e
		}
		_, copyErr := io.CopyN(out, tr, h.Size)
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return err
	}
	if limited.N <= 0 {
		return errors.New("expanded package exceeds size limit")
	}
	for _, required := range []string{"runtime.json", "bin/seekbox", "bin/gateway.py", "web/index.html", "web/version.json"} {
		if !seen[required] {
			return fmt.Errorf("package missing %s", required)
		}
	}
	if seen["auth/login.cjs"] != seen["auth/cookies.cjs"] {
		return errors.New("login and cookie helpers must be released together")
	}
	frontData, err := os.ReadFile(filepath.Join(destination, "web/version.json"))
	if err != nil {
		return err
	}
	var front struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(frontData, &front) != nil || front.Version != m.Version {
		return errors.New("frontend version does not match signed manifest")
	}
	runtimeData, err := os.ReadFile(filepath.Join(destination, "runtime.json"))
	if err != nil {
		return err
	}
	var info struct {
		Version    string `json:"version"`
		Arch       string `json:"arch"`
		DataSchema int    `json:"data_schema"`
	}
	if json.Unmarshal(runtimeData, &info) != nil || info.Version != m.Version || info.Arch != m.Arch || info.DataSchema != m.DataSchema {
		return errors.New("runtime metadata does not match signed manifest")
	}
	binary, err := elf.Open(filepath.Join(destination, "bin/seekbox"))
	if err != nil {
		return errors.New("backend is not a valid ELF executable")
	}
	defer binary.Close()
	expected := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[m.Arch]
	if expected == 0 || binary.Machine != expected || binary.Class != elf.ELFCLASS64 || (binary.Type != elf.ET_EXEC && binary.Type != elf.ET_DYN) {
		return errors.New("backend executable architecture mismatch")
	}
	return filepath.WalkDir(destination, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return syncDirectory(name)
		}
		return nil
	})
}
func syncDirectory(name string) error {
	f, e := os.Open(name)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
