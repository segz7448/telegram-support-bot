package main

import (
	"os"
	"os/exec"
	"strings"
)

// curlChildEnv returns the environment for a curl child process: the parent's environment minus the dynamic-linker
// variables LD_LIBRARY_PATH, LD_PRELOAD and LD_AUDIT. On some hosts (seen on an Android phone) the supervisor sets
// LD_LIBRARY_PATH to its own library directory, and the system curl then fails to link. Nothing else is touched:
// proxy and CA variables are kept, and TLS verification is unchanged.
func curlChildEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		switch k {
		case "LD_LIBRARY_PATH", "LD_PRELOAD", "LD_AUDIT":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func curlCommand(curl string, args ...string) *exec.Cmd {
	cmd := exec.Command(curl, args...)
	cmd.Env = curlChildEnv(os.Environ())
	return cmd
}
