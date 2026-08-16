package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type provenance struct {
	GeneratorDigest string
	SourceDigest    string
	CombinedDigest  string
	Commit          string
	Files           []string
}

func computeProvenance(m module, extra ...string) (provenance, error) {
	return computeProvenanceWithSourceCleanliness(m, true, extra...)
}

// computeProvenanceWithSourceCleanliness exists solely for isolated source
// mutation tests. Production generation always reaches it through
// computeProvenance and therefore rejects a dirty resolved producer.
func computeProvenanceWithSourceCleanliness(m module, requireCleanSource bool, extra ...string) (provenance, error) {
	if requireCleanSource {
		if err := rejectDirtyProducer(m.Dir); err != nil {
			return provenance{}, err
		}
	}
	commit, err := requireGitCommit(m.Dir)
	if err != nil {
		return provenance{}, err
	}
	files, err := provenanceInputs(m, extra...)
	if err != nil {
		return provenance{}, err
	}
	generatorPrefix := []string{}
	if toolDir, err := os.Getwd(); err == nil {
		generatorPrefix = append(generatorPrefix, filepath.Clean(toolDir))
	}
	var generatorFiles, sourceFiles []string
	for _, file := range files {
		clean := filepath.Clean(file)
		isGenerator := false
		for _, prefix := range generatorPrefix {
			if strings.HasPrefix(clean, prefix+string(os.PathSeparator)) || clean == prefix {
				isGenerator = true
				break
			}
		}
		if isGenerator {
			generatorFiles = append(generatorFiles, clean)
		} else {
			sourceFiles = append(sourceFiles, clean)
		}
	}
	genDigest, err := digestFiles(generatorFiles)
	if err != nil {
		return provenance{}, err
	}
	srcDigest, err := digestFiles(sourceFiles)
	if err != nil {
		return provenance{}, err
	}
	combined, err := digestFiles(files)
	if err != nil {
		return provenance{}, err
	}
	return provenance{
		GeneratorDigest: genDigest,
		SourceDigest:    srcDigest,
		CombinedDigest:  combined,
		Commit:          commit,
		Files:           files,
	}, nil
}

func provenanceInputs(m module, extra ...string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(path string) {
		clean := filepath.Clean(path)
		if seen[clean] {
			return
		}
		seen[clean] = true
		files = append(files, clean)
	}
	toolDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(toolDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		switch {
		case strings.HasSuffix(name, ".go"), name == "go.mod", name == "go.sum":
			add(path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, path := range extra {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			err := filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
					add(p)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		add(path)
	}
	sort.Strings(files)
	return files, nil
}

func digestFiles(files []string) (string, error) {
	h := sha256.New()
	sorted := append([]string{}, files...)
	sort.Strings(sorted)
	for _, path := range sorted {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprint(h, stableDigestPath(path))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stableDigestPath makes provenance independent of the checkout directory.
// The generator and the producer are distinct inputs, so retain their logical
// roots and all paths below them rather than hashing host-specific absolute
// directories.
func stableDigestPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	if marker := "/clientserver/tools/protocolgen/"; strings.Contains(path, marker) {
		return "generator/" + strings.SplitN(path, marker, 2)[1]
	}
	if marker := "/internal/"; strings.Contains(path, marker) {
		return "producer" + strings.SplitN(path, marker, 2)[1]
	}
	return filepath.Base(path)
}

func rejectDirtyProducer(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
		if out, runErr := cmd.Output(); runErr != nil || strings.TrimSpace(string(out)) != "true" {
			return fmt.Errorf("resolved producer %s is not a clean git tree", dir)
		}
	}
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("inspect producer git status: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("resolved producer tree is dirty")
	}
	return nil
}

func requireGitCommit(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("resolved producer commit is unknown: %w", err)
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" || commit == "unknown" {
		return "", fmt.Errorf("resolved producer commit is unknown")
	}
	return commit, nil
}
