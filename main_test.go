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
	t.Setenv("SESSION_ROTATION_INTERVAL", "15m")
	t.Setenv("SESSION_ROTATION_GRACE_PERIOD", "30s")

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
	if got.RotationInterval != 15*time.Minute {
		t.Errorf("RotationInterval = %v, want %v", got.RotationInterval, 15*time.Minute)
	}
	if got.GracePeriod != 30*time.Second {
		t.Errorf("GracePeriod = %v, want %v", got.GracePeriod, 30*time.Second)
	}
}

func TestLoadCookieConfig(t *testing.T) {
	t.Setenv("SESSION_COOKIE_SECURE", "true")
	got, err := loadCookieConfig()
	if err != nil {
		t.Fatalf("loadCookieConfig() error = %v", err)
	}
	if !got.Secure {
		t.Fatal("Secure = false, want true")
	}

	t.Setenv("SESSION_COOKIE_SECURE", "not-a-bool")
	if _, err := loadCookieConfig(); err == nil {
		t.Fatal("loadCookieConfig() accepted an invalid boolean")
	}
}
