package main

import (
	"encoding/json"
	"os"
)

func main() {
	checks := map[string]bool{}
	for _, name := range []string{"OPENROUTER_API_KEY", "OTHER_API_KEY", "OTHER_TOKEN", "AWS_SECRET_ACCESS_KEY", "SMOKE_EXCLUDED", "OUTSIDE_INCLUDE_ONLY"} {
		checks[name] = os.Getenv(name) == ""
	}
	checks["sentinelPreserved"] = os.Getenv("SMOKE_SENTINEL") == "kept"
	checks["inheritedPreserved"] = os.Getenv("SMOKE_INHERITED") == "inherited"
	ok := true
	for _, passed := range checks {
		ok = ok && passed
	}
	result := struct {
		Status string          `json:"status"`
		Checks map[string]bool `json:"checks"`
	}{"credential-isolated", checks}
	if !ok {
		result.Status = "environment-contract-failed"
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil || !ok {
		os.Exit(1)
	}
}
