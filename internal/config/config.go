package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Network struct {
		Region             string `yaml:"region"`
		WSKeepAliveSeconds int    `yaml:"ws_keepalive_seconds"`
	} `yaml:"network"`
	Logging struct {
		Level  string `yaml:"level"`
		Pretty bool   `yaml:"pretty"`
	} `yaml:"logging"`
	Server struct {
		Addr                string   `yaml:"addr"`
		Pprof               bool     `yaml:"pprof"`
		ReadTimeoutSeconds  int      `yaml:"read_timeout_seconds"`
		WriteTimeoutSeconds int      `yaml:"write_timeout_seconds"`
		IdleTimeoutSeconds  int      `yaml:"idle_timeout_seconds"`
		AdminAllowCIDRs     []string `yaml:"admin_allow_cidrs"`
	} `yaml:"server"`
	Trading struct {
		Enabled                 bool               `yaml:"enabled"`
		Live                    bool               `yaml:"live"`
		Pairs                   []string           `yaml:"pairs"`
		MinNetBps               decimal.Decimal    `yaml:"min_net_bps"`
		NotionalUSD             decimal.Decimal    `yaml:"notional_usd"`
		MaxNotionalUSD          decimal.Decimal    `yaml:"max_notional_usd"`
		MaxOrdersPerMin         int                `yaml:"max_orders_per_min"`
		AllowedSymbols          []string           `yaml:"allowed_symbols"`
		FeesBps                 map[string]float64 `yaml:"fees_bps"`
		SlippageBps             float64            `yaml:"slippage_bps"`
		RiskReserveBps          float64            `yaml:"risk_reserve_bps"`
		PriceSkewBps            float64            `yaml:"price_skew_bps"`
		EntryConfirmTicks       int                `yaml:"entry_confirm_ticks"`
		TriangleCooldownSeconds int                `yaml:"triangle_cooldown_seconds"`
		DailyPnLStopUSD         float64            `yaml:"daily_pnl_stop_usd"`
		MaxInventoryUSDPerBase  float64            `yaml:"max_inventory_usd_per_base"`
		MaxUnwindSlippageBps    float64            `yaml:"max_unwind_slippage_bps"`
		OrderTTLMs              int                `yaml:"order_ttl_ms"`
		MaxConcurrentTriangles  int                `yaml:"max_concurrent_triangles"`
		Triangles               []Triangle         `yaml:"triangles"`
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

type Triangle struct {
	AB string `yaml:"AB"`
	BC string `yaml:"BC"`
	CA string `yaml:"CA"`
}

func defaultConfig() Config {
	var c Config
	c.Network.Region = "EU-West"
	c.Network.WSKeepAliveSeconds = 15
	c.Logging.Level = "info"
	c.Logging.Pretty = false
	c.Server.Addr = ":19091"
	c.Server.Pprof = false
	c.Server.ReadTimeoutSeconds = 5
	c.Server.WriteTimeoutSeconds = 10
	c.Server.IdleTimeoutSeconds = 60
	c.Server.AdminAllowCIDRs = []string{"127.0.0.0/8", "::1/128"}
	c.Trading.Enabled = true
	c.Trading.Live = true
	c.Trading.Pairs = []string{"BTCUSDT", "ETHUSDT", "BNBUSDT", "SOLUSDT", "XRPUSDT", "ADAUSDT", "DOGEUSDT", "LTCUSDT", "TRXUSDT", "MATICUSDT", "DOTUSDT", "LINKUSDT"}
	c.Trading.MinNetBps = decimal.NewFromFloat(0.3)
	c.Trading.NotionalUSD = decimal.NewFromFloat(300.0)
	c.Trading.MaxNotionalUSD = decimal.NewFromFloat(800.0)
	c.Trading.MaxOrdersPerMin = 100
	c.Trading.AllowedSymbols = nil
	c.Trading.FeesBps = map[string]float64{"bybit": 10.0}
	c.Trading.SlippageBps = 1.0
	c.Trading.RiskReserveBps = 0.5
	c.Trading.PriceSkewBps = 1.0 // 0.01% default skew
	c.Trading.EntryConfirmTicks = 0
	c.Trading.TriangleCooldownSeconds = 1
	c.Trading.DailyPnLStopUSD = 0.0
	c.Trading.MaxInventoryUSDPerBase = 0.0
	c.Trading.MaxUnwindSlippageBps = 10.0 // 0.10% max slippage for unwind
	c.Trading.OrderTTLMs = 1500
	c.Trading.MaxConcurrentTriangles = 1
	c.Trading.Triangles = []Triangle{
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
	// Load .env file first
	loadEnvFile(".env")
	if path := os.Getenv("ARBITR_CONFIG"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = yaml.Unmarshal(b, &c)
		}
	}
	if v := os.Getenv("ARBITR_REGION"); v != "" {
		c.Network.Region = v
	}
	if v := os.Getenv("ARBITR_LOG_LEVEL"); v != "" {
		c.Logging.Level = v
	}
	if v := os.Getenv("ARBITR_HTTP_ADDR"); v != "" {
		c.Server.Addr = v
	}
	if v := os.Getenv("ARBITR_PPROF"); v == "1" || v == "true" {
		c.Server.Pprof = true
	}
	if v := os.Getenv("ARBITR_ADMIN_ALLOW_CIDRS"); v != "" {
		c.Server.AdminAllowCIDRs = splitCSV(v)
	}
	if v := os.Getenv("ARBITR_TRADING_ENABLED"); v == "1" || v == "true" {
		c.Trading.Enabled = true
	}
	if v := os.Getenv("ARBITR_TRADING_LIVE"); v == "1" || v == "true" {
		c.Trading.Live = true
	}
	if v := os.Getenv("ARBITR_TRADING_PAIRS"); v != "" {
		c.Trading.Pairs = splitCSV(v)
	}
	if v := os.Getenv("ARBITR_MAX_NOTIONAL_USD"); v != "" {
		if d, err := decimal.NewFromString(v); err == nil && d.IsPositive() {
			c.Trading.MaxNotionalUSD = d
		}
	}
	if v := os.Getenv("ARBITR_MAX_ORDERS_PER_MIN"); v != "" {
		var n int
		_, _ = fmt.Sscan(v, &n)
		if n > 0 {
			c.Trading.MaxOrdersPerMin = n
		}
	}
	if v := os.Getenv("ARBITR_ALLOWED_SYMBOLS"); v != "" {
		c.Trading.AllowedSymbols = splitCSV(v)
	}
	if v := os.Getenv("ARBITR_ENTRY_CONFIRM_TICKS"); v != "" {
		var n int
		_, _ = fmt.Sscan(v, &n)
		if n >= 0 {
			c.Trading.EntryConfirmTicks = n
		}
	}
	if v := os.Getenv("ARBITR_TRIANGLE_COOLDOWN_SECONDS"); v != "" {
		var n int
		_, _ = fmt.Sscan(v, &n)
		if n >= 0 {
			c.Trading.TriangleCooldownSeconds = n
		}
	}
	if v := os.Getenv("ARBITR_DAILY_PNL_STOP_USD"); v != "" {
		var f float64
		_, _ = fmt.Sscan(v, &f)
		if f >= 0 {
			c.Trading.DailyPnLStopUSD = f
		}
	}
	if v := os.Getenv("ARBITR_MAX_INVENTORY_USD_PER_BASE"); v != "" {
		var f float64
		_, _ = fmt.Sscan(v, &f)
		if f >= 0 {
			c.Trading.MaxInventoryUSDPerBase = f
		}
	}
	if v := os.Getenv("ARBITR_PRICE_SKEW_BPS"); v != "" {
		var f float64
		_, _ = fmt.Sscan(v, &f)
		if f >= 0 {
			c.Trading.PriceSkewBps = f
		}
	}
	if v := os.Getenv("ARBITR_MIN_NET_BPS"); v != "" {
		if d, err := decimal.NewFromString(v); err == nil && d.IsPositive() {
			c.Trading.MinNetBps = d
		}
	}
	if v := os.Getenv("ARBITR_NOTIONAL_USD"); v != "" {
		if d, err := decimal.NewFromString(v); err == nil && d.IsPositive() {
			c.Trading.NotionalUSD = d
		}
	}
	if v := os.Getenv("ARBITR_SLIPPAGE_BPS"); v != "" {
		var f float64
		_, _ = fmt.Sscan(v, &f)
		if f >= 0 {
			c.Trading.SlippageBps = f
		}
	}
	if v := os.Getenv("ARBITR_FEES_BPS_BYBIT"); v != "" {
		var f float64
		_, _ = fmt.Sscan(v, &f)
		if f >= 0 {
			c.Trading.FeesBps["bybit"] = f
		}
	}
	if v := os.Getenv("ARBITR_RISK_RESERVE_BPS"); v != "" {
		var f float64
		_, _ = fmt.Sscan(v, &f)
		if f >= 0 {
			c.Trading.RiskReserveBps = f
		}
	}
	if v := os.Getenv("ARBITR_ORDER_TTL_MS"); v != "" {
		var n int
		_, _ = fmt.Sscan(v, &n)
		if n > 0 {
			c.Trading.OrderTTLMs = n
		}
	}
	if v := os.Getenv("ARBITR_MAX_CONCURRENT_TRIANGLES"); v != "" {
		var n int
		_, _ = fmt.Sscan(v, &n)
		if n > 0 {
			c.Trading.MaxConcurrentTriangles = n
		}
	}
	if v := os.Getenv("ARBITR_MAX_UNWIND_SLIPPAGE_BPS"); v != "" {
		var f float64
		_, _ = fmt.Sscan(v, &f)
		if f >= 0 {
			c.Trading.MaxUnwindSlippageBps = f
		}
	}
	// API keys only from env
	if v := os.Getenv("ARBITR_BYBIT_API_KEY"); v != "" {
		c.Exchanges.Bybit.APIKey = v
	}
	if v := os.Getenv("ARBITR_BYBIT_SECRET"); v != "" {
		c.Exchanges.Bybit.Secret = v
	}
	// Allow overriding Bybit base URL via env to switch between testnet and mainnet easily
	if v := os.Getenv("ARBITR_BYBIT_BASE_URL"); v != "" {
		c.Exchanges.Bybit.BaseURL = v
	}
	// Triangles via YAML only for now; could add ARBITR_TRADING_TRIANGLES as a CSV of AB|BC|CA items later.
	return c
}

func splitCSV(s string) []string {
	var out []string
	buf := []rune{}
	for _, r := range s {
		if r == ',' {
			if len(buf) > 0 {
				out = append(out, string(buf))
				buf = buf[:0]
			}
			continue
		}
		buf = append(buf, r)
	}
	if len(buf) > 0 {
		out = append(out, string(buf))
	}
	return out
}

// loadEnvFile loads environment variables from .env file
func loadEnvFile(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()
	
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			os.Setenv(parts[0], parts[1])
		}
	}
}
