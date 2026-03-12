package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	Server    ServerConfig
	Storage   StorageConfig
	DBWorker  DBWorkerConfig
	Redis     RedisConfig
	NATS      NATSConfig
	Partition PartitionConfig
	Auth      AuthConfig
	RateLimit RateLimitConfig
	Simulator SimulatorConfig
	Binance   BinanceConfig
	WebSocket WebSocketConfig
	Log       LogConfig
}

type ServerConfig struct {
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

type StorageConfig struct {
	Type        string // "memory" | "postgres"
	DatabaseURL string
}

type DBWorkerConfig struct {
	Enabled       bool
	BatchSize     int
	FlushInterval time.Duration
	ChannelSize   int
	RetryMax      int
	RetryBackoff  time.Duration
}

type RedisConfig struct {
	Enabled bool
	URL     string
}

type NATSConfig struct {
	Enabled bool
	URL     string
}

// PartitionConfig controls consistent-hash partitioning of the matching engine
// across multiple instances.  When enabled, each instance runs matchers only
// for the stocks it "owns" and forwards other orders via NATS.
//
// Requirements when Enabled=true:
//   - NATS_ENABLED must also be true (orders are forwarded via NATS).
//   - InstanceIndex must be in [0, TotalInstances).
type PartitionConfig struct {
	Enabled        bool
	InstanceIndex  int // 0-based index of THIS instance
	TotalInstances int // total instances in the cluster
}

type AuthConfig struct {
	Enabled   bool
	JWTSecret string
	JWTExpiry time.Duration
}

type RateLimitConfig struct {
	Enabled bool
	RPS     float64
	Burst   int
}

type SimulatorConfig struct {
	Enabled     bool
	IntervalMin time.Duration
	IntervalMax time.Duration
}

type BinanceConfig struct {
	Enabled      bool
	WSURL        string
	Streams      string
	ReconnectMax time.Duration
}

type WebSocketConfig struct {
	WriteTimeout              time.Duration
	PongTimeout               time.Duration
	PingInterval              time.Duration
	MaxMessageSize            int64
	SendBufferSize            int
	MaxConnections            int
	MaxConnectionsPerIP       int
	MaxSubscriptionsPerClient int
	// TrustedProxyCIDRs is a comma-separated list of CIDR blocks for trusted
	// reverse proxies.  When the direct connection originates from one of these
	// CIDRs, X-Forwarded-For is trusted for real-IP extraction.
	TrustedProxyCIDRs []string
	// AllowedOrigins is a list of Origin patterns accepted during the WebSocket
	// handshake.  If empty, the library's default (same-host check) applies.
	AllowedOrigins []string
}

type LogConfig struct {
	Level  string // "debug" | "info" | "warn" | "error"
	Format string // "json" | "text"
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Port:            getEnv("SERVER_PORT", "8080"),
			ReadTimeout:     getDuration("SERVER_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:    getDuration("SERVER_WRITE_TIMEOUT", 10*time.Second),
			ShutdownTimeout: getDuration("SERVER_SHUTDOWN_TIMEOUT", 30*time.Second),
		},
		Storage: StorageConfig{
			Type:        getEnv("STORAGE_TYPE", "memory"),
			DatabaseURL: getEnv("DATABASE_URL", "postgres://miniexchange:miniexchange@localhost:5432/miniexchange?sslmode=disable"),
		},
		DBWorker: DBWorkerConfig{
			Enabled:       getBool("DB_WORKER_ENABLED", false),
			BatchSize:     getInt("DB_WORKER_BATCH_SIZE", 100),
			FlushInterval: getDuration("DB_WORKER_FLUSH_INTERVAL", 100*time.Millisecond),
			ChannelSize:   getInt("DB_WORKER_CHANNEL_SIZE", 5000),
			RetryMax:      getInt("DB_WORKER_RETRY_MAX", 3),
			RetryBackoff:  getDuration("DB_WORKER_RETRY_BACKOFF", 1*time.Second),
		},
		Redis: RedisConfig{
			Enabled: getBool("REDIS_ENABLED", false),
			URL:     getEnv("REDIS_URL", "redis://localhost:6379/0"),
		},
		NATS: NATSConfig{
			Enabled: getBool("NATS_ENABLED", false),
			URL:     getEnv("NATS_URL", "nats://localhost:4222"),
		},
		Partition: PartitionConfig{
			Enabled:        getBool("PARTITION_ENABLED", false),
			InstanceIndex:  getInt("PARTITION_INSTANCE_INDEX", 0),
			TotalInstances: getInt("PARTITION_TOTAL_INSTANCES", 1),
		},
		Auth: AuthConfig{
			Enabled:   getBool("AUTH_ENABLED", false),
			JWTSecret: getEnv("JWT_SECRET", "change-me-in-production"),
			JWTExpiry: getDuration("JWT_EXPIRY", 24*time.Hour),
		},
		RateLimit: RateLimitConfig{
			Enabled: getBool("RATE_LIMIT_ENABLED", true),
			RPS:     getFloat64("RATE_LIMIT_RPS", 100),
			Burst:   getInt("RATE_LIMIT_BURST", 200),
		},
		Simulator: SimulatorConfig{
			Enabled:     getBool("SIMULATOR_ENABLED", true),
			IntervalMin: getDuration("SIMULATOR_INTERVAL_MIN", 1*time.Second),
			IntervalMax: getDuration("SIMULATOR_INTERVAL_MAX", 3*time.Second),
		},
		Binance: BinanceConfig{
			Enabled:      getBool("BINANCE_FEED_ENABLED", false),
			WSURL:        getEnv("BINANCE_WS_URL", "wss://stream.binance.com:9443"),
			Streams:      getEnv("BINANCE_STREAMS", "btcusdt@miniTicker/ethusdt@miniTicker/bnbusdt@miniTicker/solusdt@miniTicker/adausdt@miniTicker"),
			ReconnectMax: getDuration("BINANCE_RECONNECT_MAX", 30*time.Second),
		},
		WebSocket: WebSocketConfig{
			WriteTimeout:              getDuration("WS_WRITE_TIMEOUT", 10*time.Second),
			PongTimeout:               getDuration("WS_PONG_TIMEOUT", 60*time.Second),
			PingInterval:              getDuration("WS_PING_INTERVAL", 54*time.Second),
			MaxMessageSize:            int64(getInt("WS_MAX_MESSAGE_SIZE", 4096)),
			SendBufferSize:            getInt("WS_SEND_BUFFER_SIZE", 256),
			MaxConnections:            getInt("WS_MAX_CONNECTIONS", 1000),
			MaxConnectionsPerIP:       getInt("WS_MAX_CONNECTIONS_PER_IP", 10),
			MaxSubscriptionsPerClient: getInt("WS_MAX_SUBSCRIPTIONS_PER_CLIENT", 20),
			TrustedProxyCIDRs:         getStringSlice("WS_TRUSTED_PROXY_CIDRS", nil),
			AllowedOrigins:            getStringSlice("WS_ALLOWED_ORIGINS", nil),
		},
		Log: LogConfig{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
		},
	}
}

// Validate checks that required configuration values are set.
func (c *Config) Validate() error {
	if c.Auth.Enabled && c.Auth.JWTSecret == "change-me-in-production" {
		return fmt.Errorf("JWT_SECRET must be changed in production when AUTH_ENABLED=true")
	}
	if c.Storage.Type != "memory" && c.Storage.Type != "postgres" {
		return fmt.Errorf("STORAGE_TYPE must be 'memory' or 'postgres', got: %s", c.Storage.Type)
	}
	if c.Storage.Type == "postgres" && c.Storage.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required when STORAGE_TYPE=postgres")
	}
	if c.Simulator.IntervalMin > c.Simulator.IntervalMax {
		return fmt.Errorf("SIMULATOR_INTERVAL_MIN must be <= SIMULATOR_INTERVAL_MAX")
	}
	if c.Partition.Enabled {
		if c.Partition.TotalInstances < 1 {
			return fmt.Errorf("PARTITION_TOTAL_INSTANCES must be >= 1")
		}
		if c.Partition.InstanceIndex < 0 || c.Partition.InstanceIndex >= c.Partition.TotalInstances {
			return fmt.Errorf("PARTITION_INSTANCE_INDEX %d is out of range [0, %d)",
				c.Partition.InstanceIndex, c.Partition.TotalInstances)
		}
		if !c.NATS.Enabled {
			return fmt.Errorf("NATS_ENABLED must be true when PARTITION_ENABLED=true (orders are forwarded via NATS)")
		}
	}
	return nil
}

// --- helpers ---

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getBool(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultVal
	}
	return b
}

func getInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return i
}

func getFloat64(key string, defaultVal float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultVal
	}
	return f
}

func getDuration(key string, defaultVal time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultVal
	}
	return d
}

// getStringSlice returns a slice of non-empty strings split by comma from the
// environment variable key. Returns defaultVal if the variable is unset or empty.
func getStringSlice(key string, defaultVal []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return defaultVal
	}
	return out
}
