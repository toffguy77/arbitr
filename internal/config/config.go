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
	Trading struct {
		Enabled bool    `yaml:"enabled"`
		Pairs   []string `yaml:"pairs"`
		MinNetBps float64 `yaml:"min_net_bps"`
		NotionalUSD float64 `yaml:"notional_usd"`
		FeesBps map[string]float64 `yaml:"fees_bps"`
		SlippageBps float64 `yaml:"slippage_bps"`
		RiskReserveBps float64 `yaml:"risk_reserve_bps"`
		Triangles []struct{ AB, BC, CA string } `yaml:"triangles"`
	} `yaml:"trading"`
	Exchanges struct {
		Bybit struct {
			BaseURL string `yaml:"base_url"`
			APIKey  string `yaml:"api_key"`
			Secret  string `yaml:"secret"`
		} `yaml:"bybit"`
		Binance struct {
			BaseURL string `yaml:"base_url"`
			APIKey  string `yaml:"api_key"`
			Secret  string `yaml:"secret"`
		} `yaml:"binance"`
		Kraken struct {
			BaseURL string `yaml:"base_url"`
			APIKey  string `yaml:"api_key"`
			Secret  string `yaml:"secret"`
		} `yaml:"kraken"`
	} `yaml:"exchanges"`
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
	c.Trading.Enabled = false
	c.Trading.Pairs = []string{"BTCUSDT","ETHUSDT","BNBUSDT","SOLUSDT","XRPUSDT","ADAUSDT","DOGEUSDT","LTCUSDT","TRXUSDT","MATICUSDT","DOTUSDT","LINKUSDT"}
	c.Trading.MinNetBps = 5.0
	c.Trading.NotionalUSD = 100.0
	c.Trading.FeesBps = map[string]float64{"bybit": 10.0}
	c.Trading.SlippageBps = 1.0
	c.Trading.RiskReserveBps = 0.5
	c.Trading.Triangles = []struct{ AB, BC, CA string }{
		{AB: "BTCUSDT", BC: "ETHUSDT", CA: "ETHBTC"},
		{AB: "BTCUSDT", BC: "BNBUSDT", CA: "BNBBTC"},
		{AB: "BTCUSDT", BC: "SOLUSDT", CA: "SOLBTC"},
		{AB: "BTCUSDT", BC: "XRPUSDT", CA: "XRPBTC"},
		{AB: "BTCUSDT", BC: "ADAUSDT", CA: "ADABTC"},
		{AB: "BTCUSDT", BC: "DOGEUSDT", CA: "DOGEBTC"},
		{AB: "BTCUSDT", BC: "LTCUSDT", CA: "LTCBTC"},
		{AB: "BTCUSDT", BC: "TRXUSDT", CA: "TRXBTC"},
		{AB: "BTCUSDT", BC: "MATICUSDT", CA: "MATICBTC"},
		{AB: "BTCUSDT", BC: "DOTUSDT", CA: "DOTBTC"},
		{AB: "BTCUSDT", BC: "LINKUSDT", CA: "LINKBTC"},
	}
	c.Exchanges.Bybit.BaseURL = "https://api.bybit.com"
	c.Exchanges.Binance.BaseURL = "https://api.binance.com"
	c.Exchanges.Kraken.BaseURL = "https://api.kraken.com"
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
	if v := os.Getenv("ARBITR_TRADING_ENABLED"); v == "1" || v == "true" { c.Trading.Enabled = true }
	if v := os.Getenv("ARBITR_TRADING_PAIRS"); v != "" { c.Trading.Pairs = splitCSV(v) }
	// Triangles via YAML only for now; could add ARBITR_TRADING_TRIANGLES as a CSV of AB|BC|CA items later.
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
