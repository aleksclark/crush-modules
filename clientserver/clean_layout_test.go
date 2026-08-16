package clientserver_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCleanLayoutSimulation starts with the tracked base archive, overlays only
// the intended public module, and clones the producer at the exact sibling path
// required by the tooling replace. It prevents ambient worktree files from
// making the drift or public-module gates pass accidentally.
func TestCleanLayoutSimulation(t *testing.T) {
	if os.Getenv("CLIENTSERVER_CLEAN_LAYOUT") == "1" {
		t.Skip("already running inside the clean-layout simulation")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	cleanRoot := filepath.Join(tmp, "crush-modules")
	gitArchive(t, root, cleanRoot)
	copyTree(t, filepath.Join(root, "clientserver"), filepath.Join(cleanRoot, "clientserver"))

	producer := producerReplacementDir(t)
	gitClone(t, producer, filepath.Join(tmp, "crush-plugin-poc"))
	toolDir := filepath.Join(cleanRoot, "clientserver", "tools", "protocolgen")
	runCleanLayout(t, toolDir, "go", "-C", toolDir, "test", "./...")
	runCleanLayout(t, cleanRoot, "go", "-C", toolDir, "run", ".", "-out", "../..", "-check")
	runCleanLayout(t, filepath.Join(cleanRoot, "clientserver"), "go", "test", "./...")
}

func producerReplacementDir(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "-C", filepath.Join("tools", "protocolgen"), "list", "-m", "-f", "{{.Dir}}", "github.com/charmbracelet/crush")
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("resolve producer replacement: %v", err)
	}
	return stringTrimSpace(string(out))
}

func gitArchive(t *testing.T, root, dest string) {
	t.Helper()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := exec.Command("git", "-C", root, "archive", "--format=tar", "HEAD")
	out, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	untar := exec.Command("tar", "-x", "-C", dest)
	untar.Stdin = out
	if err := archive.Start(); err != nil {
		t.Fatal(err)
	}
	if err := untar.Run(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Wait(); err != nil {
		t.Fatal(err)
	}
}

func gitClone(t *testing.T, source, dest string) {
	t.Helper()
	cmd := exec.Command("git", "clone", "--quiet", "--no-local", source, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone clean producer: %v\n%s", err, out)
	}
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	}); err != nil {
		t.Fatalf("overlay intended clientserver files: %v", err)
	}
}

func runCleanLayout(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLIENTSERVER_CLEAN_LAYOUT=1", "GOTMPDIR=", "TMPDIR=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clean layout %s %v: %v\n%s", name, args, err, out)
	}
}

func stringTrimSpace(value string) string {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\t' || value[start] == '\r') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\t' || value[end-1] == '\r') {
		end--
	}
	return value[start:end]
}
