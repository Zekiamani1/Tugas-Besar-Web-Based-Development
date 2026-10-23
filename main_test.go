package main

import (
	"testing"
	"time"
)

func TestParsePositiveDurationEnv(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "valid duration", value: "30m", want: 30 * time.Minute},
		{name: "empty value", value: "", wantErr: true},
		{name: "invalid value", value: "later", wantErr: true},
		{name: "zero duration", value: "0s", wantErr: true},
		{name: "negative duration", value: "-1m", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TEST_SESSION_DURATION", test.value)

			got, err := parsePositiveDurationEnv("TEST_SESSION_DURATION")
			if (err != nil) != test.wantErr {
				t.Fatalf("parsePositiveDurationEnv() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Errorf("parsePositiveDurationEnv() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestLoadSessionConfig(t *testing.T) {
	t.Setenv("SESSION_ABSOLUTE_TTL", "12h")
	t.Setenv("SESSION_IDLE_TIMEOUT", "30m")

	got, err := loadSessionConfig()
	if err != nil {
		t.Fatalf("loadSessionConfig() error = %v", err)
	}
	if got.AbsoluteTTL != 12*time.Hour {
		t.Errorf("AbsoluteTTL = %v, want %v", got.AbsoluteTTL, 12*time.Hour)
	}
	if got.IdleTimeout != 30*time.Minute {
		t.Errorf("IdleTimeout = %v, want %v", got.IdleTimeout, 30*time.Minute)
	}
}
