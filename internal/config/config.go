// Package config loads server settings from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AIBaseURL     string
	AIAPIKey      string
	AIModelA      string
	AIModelB      string
	MockAI        bool
	AppAPIKey     string
	AllowedOrigin string
	Port          string
	PinTTL        time.Duration
}

// Load reads the config from the environment. Outside mock mode every value
// the model call and API protection need is required.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	c := Config{
		AIBaseURL:     strings.TrimRight(getenv("AI_BASE_URL"), "/"),
		AIAPIKey:      getenv("AI_API_KEY"),
		AIModelA:      getenv("AI_MODEL_A"),
		AIModelB:      getenv("AI_MODEL_B"),
		AppAPIKey:     getenv("APP_API_KEY"),
		AllowedOrigin: getenv("ALLOWED_ORIGIN"),
		Port:          getenv("PORT"),
	}
	if c.Port == "" {
		c.Port = "8080"
	}
	c.PinTTL = 3 * time.Minute
	if v := getenv("PIN_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("PIN_TTL: not a positive duration: %q", v)
		}
		c.PinTTL = d
	}
	if v := getenv("MOCK_AI"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("MOCK_AI: not a boolean: %q", v)
		}
		c.MockAI = b
	}
	if c.MockAI {
		return c, nil
	}

	var missing []string
	for _, kv := range []struct{ name, val string }{
		{"AI_BASE_URL", c.AIBaseURL},
		{"AI_API_KEY", c.AIAPIKey},
		{"AI_MODEL_A", c.AIModelA},
		{"APP_API_KEY", c.AppAPIKey},
		{"ALLOWED_ORIGIN", c.AllowedOrigin},
	} {
		if kv.val == "" {
			missing = append(missing, kv.name)
		}
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("missing required env: %s (or set MOCK_AI=true)", strings.Join(missing, ", "))
	}
	return c, nil
}
