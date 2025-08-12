package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Network struct {
		Region string `yaml:"region"`
		WSKeepAliveSeconds int `yaml:"ws_keepalive_seconds"`
	} `yaml:"network"`
	Logging struct {
		Level string `yaml:"level"`
		Pretty bool   `yaml:"pretty"`
	} `yaml:"logging"`
	Server struct {
		Addr       string `yaml:"addr"`
		Pprof      bool   `yaml:"pprof"`
		ReadTimeoutSeconds  int `yaml:"read_timeout_seconds"`
		WriteTimeoutSeconds int `yaml:"write_timeout_seconds"`
		IdleTimeoutSeconds  int `yaml:"idle_timeout_seconds"`
		AdminAllowCIDRs     []string `yaml:"admin_allow_cidrs"`
	} `yaml:"server"`
}

func defaultConfig() Config {
	var c Config
	c.Network.Region = "EU-West"
	c.Network.WSKeepAliveSeconds = 15
	c.Logging.Level = "info"
	c.Logging.Pretty = false
	c.Server.Addr = ":9090"
	c.Server.Pprof = false
	c.Server.ReadTimeoutSeconds = 5
	c.Server.WriteTimeoutSeconds = 10
	c.Server.IdleTimeoutSeconds = 60
	c.Server.AdminAllowCIDRs = []string{"127.0.0.0/8", "::1/128"}
	return c
}

func Load() Config {
	c := defaultConfig()
	if path := os.Getenv("ARBITR_CONFIG"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = yaml.Unmarshal(b, &c)
		}
	}
	if v := os.Getenv("ARBITR_REGION"); v != "" { c.Network.Region = v }
	if v := os.Getenv("ARBITR_LOG_LEVEL"); v != "" { c.Logging.Level = v }
	if v := os.Getenv("ARBITR_HTTP_ADDR"); v != "" { c.Server.Addr = v }
	if v := os.Getenv("ARBITR_PPROF"); v == "1" || v == "true" { c.Server.Pprof = true }
	if v := os.Getenv("ARBITR_ADMIN_ALLOW_CIDRS"); v != "" { c.Server.AdminAllowCIDRs = splitCSV(v) }
	return c
}

func splitCSV(s string) []string {
	var out []string
	buf := []rune{}
	for _, r := range s {
		if r == ',' {
			if len(buf) > 0 { out = append(out, string(buf)); buf = buf[:0] }
			continue
		}
		buf = append(buf, r)
	}
	if len(buf) > 0 { out = append(out, string(buf)) }
	return out
}
