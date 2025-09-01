package config

import (
    "os"
    "testing"
)

func TestDefaultConfig(t *testing.T) {
    _ = os.Unsetenv("ARBITR_CONFIG")
    _ = os.Unsetenv("ARBITR_REGION")
    _ = os.Unsetenv("ARBITR_LOG_LEVEL")

    c := Load()
    if c.Network.Region != "EU-West" {
        t.Fatalf("expected default region EU-West, got %s", c.Network.Region)
    }
    if c.Logging.Level != "info" {
        t.Fatalf("expected default log level info, got %s", c.Logging.Level)
    }
}

func TestEnvOverrides(t *testing.T) {
    t.Setenv("ARBITR_REGION", "EU-Central")
    t.Setenv("ARBITR_LOG_LEVEL", "debug")
    c := Load()
    if c.Network.Region != "EU-Central" {
        t.Fatalf("env override failed for region, got %s", c.Network.Region)
    }
    if c.Logging.Level != "debug" {
        t.Fatalf("env override failed for log level, got %s", c.Logging.Level)
    }
}
