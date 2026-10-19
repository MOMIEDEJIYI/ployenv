package runtime

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"
)

// helperTimeout bounds every helper process so a misbehaving child can never
// hang an install.
const helperTimeout = 30 * time.Second

// captureOutput runs bin with args and returns combined stdout+stderr.
func captureOutput(bin string, args ...string) (string, error) {
	return captureOutputEnv(os.Environ(), bin, args...)
}

// captureOutputEnv is captureOutput with an explicit environment, for helpers
// whose dependencies are managed rather than inherited (Maven's JDK).
func captureOutputEnv(env []string, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
