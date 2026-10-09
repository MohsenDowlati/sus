package config

import (
	"strings"
	"testing"
)

func TestNewEnvCreateLinkRateLimit(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  int
	}{
		{name: "default", value: "", want: 20},
		{name: "benchmark override", value: "100000", want: 100000},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CREATE_LINK_RATE_LIMIT_PER_MINUTE", test.value)
			if got := NewEnv().CreateLinkRateLimit; got != test.want {
				t.Fatalf("CreateLinkRateLimit = %d, want %d", got, test.want)
			}
		})
	}
}

func TestValidateRejectsNonPositiveCreateLinkRateLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		env := &Env{
			AccessTokenSecret:  strings.Repeat("a", 32),
			RefreshTokenSecret: strings.Repeat("b", 32),
			ContextTimeout:    10,
			CreateLinkRateLimit: limit,
		}
		err := env.Validate()
		if err == nil || err.Error() != "CREATE_LINK_RATE_LIMIT_PER_MINUTE must be positive" {
			t.Fatalf("limit %d: Validate() = %v", limit, err)
		}
	}
}
