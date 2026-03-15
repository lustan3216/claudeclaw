package runner

import (
	"os"
	"strings"
)

// filteredEnv returns the current process environment with CLAUDECODE vars removed
// (to prevent claude from refusing nested launches), optionally overriding ANTHROPIC_API_KEY.
func filteredEnv(apiKey string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.Contains(e, "CLAUDECODE") {
			continue
		}
		// Remove existing ANTHROPIC_API_KEY if we're injecting a specific one
		if apiKey != "" && strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			continue
		}
		out = append(out, e)
	}
	if apiKey != "" {
		out = append(out, "ANTHROPIC_API_KEY="+apiKey)
	}
	return out
}
