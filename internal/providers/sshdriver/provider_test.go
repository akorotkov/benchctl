package sshdriver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dbarena/benchctl/internal/config"
	"github.com/dbarena/benchctl/internal/engine"
	"github.com/dbarena/benchctl/internal/tofustate"
)

// installFakeTofu puts a fake `tofu` executable at the front of PATH that
// exits with the given code for each subcommand (defaulting to 0), so tests
// can exercise Provision/Teardown's control flow without a real OpenTofu
// binary or any cloud credentials.
func installFakeTofu(t *testing.T, exitCodes map[string]int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tofu script requires a POSIX shell")
	}

	// Provision roots its work dir under $HOME (tofustate.RunDir). Isolate it so
	// tests in other packages that run concurrently and share the same run ID
	// cannot clobber this test's files.
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n"
	for cmd, code := range exitCodes {
		script += fmt.Sprintf("  %s) exit %d ;;\n", cmd, code)
	}
	script += "  output) echo '{}' ;;\n  *) exit 0 ;;\nesac\n"

	path := filepath.Join(dir, "tofu")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tofu: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestProvision_ApplyFailsDestroySucceeds_RemovesWorkDir(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	installFakeTofu(t, map[string]int{"apply": 1})

	moduleDir := filepath.Join(tmp, "module")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("mkdir module dir: %v", err)
	}
	dummyBinary := filepath.Join(tmp, "benchctl-linux-amd64")
	if err := os.WriteFile(dummyBinary, []byte("dummy"), 0o755); err != nil {
		t.Fatalf("write dummy benchctl binary: %v", err)
	}

	const runID = "test-run-20260101-000000-abcdef"
	p := New(Config{ProviderName: "test", Benchctl: &config.Config{}})
	p.out = &bytes.Buffer{}

	cfg := map[string]any{
		"module":          moduleDir,
		"benchctl_binary": dummyBinary,
	}
	outputs, err := p.Provision(context.Background(), runID, cfg)
	if err == nil {
		t.Fatal("Provision: want error from failed apply, got nil")
	}
	if outputs != nil {
		t.Fatalf("Provision: want nil outputs (destroy succeeded, no infra left), got %v", outputs)
	}

	workDir := filepath.Join(tofustate.RunDir(runID), filepath.Base(moduleDir))
	if _, statErr := os.Stat(workDir); !os.IsNotExist(statErr) {
		t.Errorf("workdir %s: want removed, stat err = %v", workDir, statErr)
	}
	runDir := tofustate.RunDir(runID)
	if _, statErr := os.Stat(runDir); !os.IsNotExist(statErr) {
		t.Errorf("run dir %s: want removed, stat err = %v", runDir, statErr)
	}
}

// TestProvision_ReadTofuOutputsFailsAfterApply_ReturnsWorkDirForTeardown is
// the regression test for the leak class this fix addresses: apply already
// succeeded (real infra exists) by the time `tofu output` fails, so
// Provision must return enough outputs for `benchctl teardown` to destroy it
// later, not a bare error that leaves it with no local or remote record.
func TestProvision_ReadTofuOutputsFailsAfterApply_ReturnsWorkDirForTeardown(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	installFakeTofu(t, map[string]int{"output": 1})

	moduleDir := filepath.Join(tmp, "module")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("mkdir module dir: %v", err)
	}
	dummyBinary := filepath.Join(tmp, "benchctl-linux-amd64")
	if err := os.WriteFile(dummyBinary, []byte("dummy"), 0o755); err != nil {
		t.Fatalf("write dummy benchctl binary: %v", err)
	}

	const runID = "test-run-20260101-000000-abcdef"
	p := New(Config{ProviderName: "test", Benchctl: &config.Config{}})
	p.out = &bytes.Buffer{}

	cfg := map[string]any{
		"module":          moduleDir,
		"benchctl_binary": dummyBinary,
	}
	outputs, err := p.Provision(context.Background(), runID, cfg)
	if err == nil {
		t.Fatal("Provision: want error from failed tofu output, got nil")
	}

	workDir := filepath.Join(tofustate.RunDir(runID), filepath.Base(moduleDir))
	absWorkDir, err2 := filepath.Abs(workDir)
	if err2 != nil {
		t.Fatalf("filepath.Abs: %v", err2)
	}
	if outputs == nil || outputs[engine.OutputKeyTofuWorkDir] != absWorkDir {
		t.Fatalf("Provision: want outputs[%s]=%s (apply succeeded, real infra exists), got %v",
			engine.OutputKeyTofuWorkDir, absWorkDir, outputs)
	}

	// Unlike the apply-failure path, nothing destroyed the infra here, so its
	// tfstate must remain on disk for a later `benchctl teardown` to use.
	if _, statErr := os.Stat(workDir); statErr != nil {
		t.Errorf("workdir %s: want present for later teardown, stat err = %v", workDir, statErr)
	}
}

func TestSyncTFFiles_CopiesSymlinkedShellScript(t *testing.T) {
	srcDir := t.TempDir()
	sharedDir := t.TempDir()
	dstDir := t.TempDir()

	sharedScript := filepath.Join(sharedDir, "reduce-variability.sh")
	if err := os.WriteFile(sharedScript, []byte("#!/bin/bash\necho hi\n"), 0o644); err != nil {
		t.Fatalf("write shared script: %v", err)
	}
	if err := os.Symlink(sharedScript, filepath.Join(srcDir, "reduce-variability.sh")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.tf"), []byte("# noop\n"), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "ignored.txt"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatalf("write ignored.txt: %v", err)
	}

	if err := syncTFFiles(srcDir, dstDir); err != nil {
		t.Fatalf("syncTFFiles: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "reduce-variability.sh"))
	if err != nil {
		t.Fatalf("read synced script: %v", err)
	}
	if string(got) != "#!/bin/bash\necho hi\n" {
		t.Errorf("synced script content = %q, want shared script content", got)
	}
	if fi, err := os.Lstat(filepath.Join(dstDir, "reduce-variability.sh")); err != nil {
		t.Fatalf("lstat synced script: %v", err)
	} else if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("synced script is still a symlink; want a real file")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "main.tf")); err != nil {
		t.Errorf("main.tf not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "ignored.txt")); !os.IsNotExist(err) {
		t.Errorf("ignored.txt should not have been copied, err = %v", err)
	}
}

func newExistingHostProvider(out *bytes.Buffer) *Provider {
	p := New(Config{
		ProviderName:   "ec2-target",
		DefaultSSHUser: "ubuntu",
		ArchFromInstanceType: func(instanceType string) string {
			if instanceType == "c8gd.metal-24xl" {
				return "arm64"
			}
			return "amd64"
		},
		Benchctl:     &config.Config{},
		ExistingHost: true,
	})
	p.out = out
	return p
}

func TestProvision_ExistingHost_ReturnsHostWithoutTofu(t *testing.T) {
	// Any tofu call would fail the test: none must happen for an existing host.
	installFakeTofu(t, map[string]int{"init": 1, "apply": 1, "output": 1})
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "benchctl-linux-arm64")
	if err := os.WriteFile(bin, []byte("dummy"), 0o755); err != nil {
		t.Fatalf("write dummy binary: %v", err)
	}

	var out bytes.Buffer
	p := newExistingHostProvider(&out)
	got, err := p.Provision(context.Background(), "test-run-20260101-000000-abcdef", map[string]any{
		"host":                 "203.0.113.7",
		"ssh_private_key_path": "/keys/target.pem",
		"instance_type":        "c8gd.metal-24xl",
		"benchctl_binary":      bin,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	want := engine.Outputs{
		"public_ip":            "203.0.113.7",
		"ssh_private_key_path": "/keys/target.pem",
		"ssh_user":             "ubuntu",
	}
	if len(got) != len(want) {
		t.Fatalf("outputs = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("outputs[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got[engine.OutputKeyTofuWorkDir]; ok {
		t.Errorf("outputs carry a tofu work dir for a host nothing provisioned: %v", got)
	}
}

func TestProvision_ExistingHost_RequiresHostAndKey(t *testing.T) {
	var out bytes.Buffer
	p := newExistingHostProvider(&out)
	_, err := p.Provision(context.Background(), "test-run-20260101-000000-abcdef", map[string]any{
		"remote_binary_source": "fetch",
	})
	if err == nil {
		t.Fatal("Provision succeeded without host and ssh_private_key_path")
	}
	for _, want := range []string{"config.host", "config.ssh_private_key_path"} {
		if !bytes.Contains([]byte(err.Error()), []byte(want)) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestTeardown_ExistingHost_LeavesHostAlone(t *testing.T) {
	installFakeTofu(t, map[string]int{"destroy": 1})
	var out bytes.Buffer
	p := newExistingHostProvider(&out)
	if err := p.Teardown(context.Background(), engine.Outputs{"public_ip": "203.0.113.7"}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}

func TestTargetArch_ExistingHostReadsInstanceType(t *testing.T) {
	var out bytes.Buffer
	p := newExistingHostProvider(&out)
	if got := p.targetArch(map[string]any{"instance_type": "c8gd.metal-24xl"}); got != "arm64" {
		t.Errorf("targetArch = %q, want arm64", got)
	}
}
