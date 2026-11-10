// Standalone release builder; uses only Go's standard library.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"debug/elf"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}
func run() error {
	root := flag.String("root", ".", "application package root (contains app/bin and app/web)")
	out := flag.String("out", "build/online-release", "output directory")
	version := flag.String("version", "", "release version, e.g. 1.6.0")
	arch := flag.String("arch", "amd64", "amd64 or arm64")
	channel := flag.String("channel", "stable", "release channel")
	keyID := flag.String("key-id", "", "trusted key identifier")
	keyFile := flag.String("private-key", "", "external PKCS8 Ed25519 PEM signing key")
	generate := flag.Bool("generate-key", false, "generate a signing key and trust JSON; refuses existing files")
	notes := flag.String("notes", "", "release notes")
	notesFile := flag.String("notes-file", "", "UTF-8 release notes file")
	flag.Parse()
	if *keyFile == "" || *keyID == "" {
		return errors.New("private-key and key-id are required")
	}
	if *generate {
		return generateKey(*keyFile, *keyID)
	}
	if !regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`).MatchString(*version) {
		return errors.New("version must be numeric major.minor.patch")
	}
	if *arch != "amd64" && *arch != "arm64" {
		return errors.New("unsupported architecture")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString(*channel) {
		return errors.New("invalid channel")
	}
	if *notesFile != "" {
		data, e := os.ReadFile(*notesFile)
		if e != nil {
			return e
		}
		*notes = string(data)
	}
	keyBytes, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(keyBytes)
	if block == nil {
		return errors.New("signing key must be PKCS8 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return errors.New("signing key must be Ed25519")
	}
	if err = os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	app := filepath.Join(*root, "app")
	paths := []string{"bin/seekbox", "bin/gateway.py"}
	for _, name := range []string{"auth/login.cjs", "auth/cookies.cjs"} {
		if _, e := os.Stat(filepath.Join(app, name)); e == nil {
			paths = append(paths, name)
		}
	}
	if err = filepath.WalkDir(filepath.Join(app, "web"), func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("release assets must not be symlinks")
		}
		if !entry.IsDir() {
			rel, e := filepath.Rel(app, path)
			if e != nil {
				return e
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(app, "web/index.html")); err != nil {
		return errors.New("build frontend before packaging")
	}
	frontBytes, e := os.ReadFile(filepath.Join(app, "web/version.json"))
	if e != nil {
		return errors.New("frontend build version.json is required")
	}
	var front struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(frontBytes, &front) != nil || front.Version != *version {
		return errors.New("frontend version does not match release version; rebuild with SEEKBOX_VERSION")
	}
	exe, e := elf.Open(filepath.Join(app, "bin/seekbox"))
	if e != nil {
		return errors.New("backend must be an ELF executable")
	}
	machine := elf.EM_X86_64
	if *arch == "arm64" {
		machine = elf.EM_AARCH64
	}
	validArch := exe.Machine == machine && exe.Class == elf.ELFCLASS64
	exe.Close()
	if !validArch {
		return errors.New("backend architecture does not match release architecture")
	}
	_, loginErr := os.Stat(filepath.Join(app, "auth/login.cjs"))
	_, cookieErr := os.Stat(filepath.Join(app, "auth/cookies.cjs"))
	if (loginErr == nil) != (cookieErr == nil) {
		return errors.New("login and cookie helpers must be packaged together")
	}
	sort.Strings(paths)
	filename := fmt.Sprintf("fn-seekbox-%s-%s.tar.gz", *version, *arch)
	output := filepath.Join(*out, filename)
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		file.Close()
		if !success {
			os.Remove(output)
		}
	}()
	digest := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(file, digest))
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte, mode int64) error {
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}); e != nil {
			return e
		}
		_, e := tw.Write(data)
		return e
	}
	var expanded int64
	for _, name := range paths {
		path := filepath.Join(app, filepath.FromSlash(name))
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular asset: %s", name)
		}
		expanded += info.Size()
		if expanded > 512<<20 {
			return errors.New("unpacked release exceeds 512 MiB")
		}
		bytes, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		mode := int64(0644)
		if name == "bin/seekbox" {
			mode = 0755
		}
		if e = add(name, bytes, mode); e != nil {
			return e
		}
	}
	meta, _ := json.Marshal(map[string]any{"version": *version, "arch": *arch, "data_schema": 1})
	if err = add("runtime.json", meta, 0644); err != nil {
		return err
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	info, err := os.Stat(output)
	if err != nil {
		return err
	}
	if info.Size() > 256<<20 {
		return errors.New("compressed release exceeds 256 MiB")
	}
	manifest := map[string]any{"schema": 1, "app": "fn-seekbox", "version": *version, "channel": *channel, "arch": *arch, "min_bootstrap": 1, "data_schema": 1, "sha256": hex.EncodeToString(digest.Sum(nil)), "size": info.Size(), "notes": *notes}
	payload, _ := json.Marshal(manifest)
	signed := map[string]string{"key_id": *keyID, "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))}
	encoded, _ := json.MarshalIndent(signed, "", "  ")
	if err = os.WriteFile(filepath.Join(*out, "release.json"), encoded, 0644); err != nil {
		return err
	}
	success = true
	fmt.Println(output)
	fmt.Println(filepath.Join(*out, "release.json"))
	return nil
}
func generateKey(file, id string) error {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: bytes})
	_, err = f.Write(data)
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	trust, _ := json.MarshalIndent(map[string]string{id: base64.StdEncoding.EncodeToString(pub)}, "", "  ")
	f, err = os.OpenFile(file+".public.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(trust)
	ce = f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	fmt.Println("Signing key created; keep it outside the repository and distributed packages.")
	fmt.Println("Trust configuration:", file+".public.json")
	return nil
}
