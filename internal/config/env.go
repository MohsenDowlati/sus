package config

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Env holds all runtime configuration, loaded from the process environment
// (and, outside production, from a local .env file). Field groups: app/server,
// MongoDB, Redis, and JWT settings.
type Env struct {
	AppEnv        string
	ServerAddress string
	LogLevel      string

	ContextTimeout    int
	ShortenerDomains  []string
	TrustedProxyCIDRs []string
	AnalyticsIPSalt   string

	MongoURI     string
	DBHost       string
	DBPort       string
	DBUser       string
	DBPass       string
	DBName       string
	DBAuthSource string

	RedisAddr         string
	RedisPassword     string
	RedisDB           int
	RedisPoolSize     int
	RedisMinIdleConns int
	RedisDialTimeout  time.Duration
	RedisReadTimeout  time.Duration
	RedisWriteTimeout time.Duration

	RedisAnalyticsStream        string
	RedisAnalyticsConsumerGroup string
	RedisAnalyticsConsumerName  string
	AnalyticsBatchSize          int
	AnalyticsPollTimeout        time.Duration
	AnalyticsMaxRetries         int
	AnalyticsRetryBackoff       time.Duration
	AnalyticsPendingMinIdle     time.Duration
	AnalyticsProcessTimeout     time.Duration
	ClickEventsRetentionDays    int
	AnalyticsMaxQueryDays       int

	AccessTokenSecret      string
	RefreshTokenSecret     string
	AccessTokenExpiryHour  int
	RefreshTokenExpiryHour int
}

// Validate enforces the invariants the application relies on at startup.
func (e *Env) Validate() error {
	if e == nil {
		return errors.New("environment is required")
	}
	if len(e.AccessTokenSecret) < 32 {
		return errors.New("ACCESS_TOKEN_SECRET must be at least 32 characters")
	}
	if len(e.RefreshTokenSecret) < 32 {
		return errors.New("REFRESH_TOKEN_SECRET must be at least 32 characters")
	}
	if e.ContextTimeout <= 0 {
		return errors.New("CONTEXT_TIMEOUT must be positive")
	}
	if len(e.ShortenerDomains) == 0 {
		return errors.New("SHORTENER_DOMAINS must contain at least one domain")
	}
	if len(e.AnalyticsIPSalt) < 32 {
		return errors.New("ANALYTICS_IP_SALT must be at least 32 characters")
	}
	for _, cidr := range e.TrustedProxyCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("invalid TRUSTED_PROXY_CIDRS entry %q: %w", cidr, err)
		}
	}
	if e.AccessTokenExpiryHour <= 0 || e.RefreshTokenExpiryHour <= 0 {
		return errors.New("token expiry values must be positive")
	}
	if strings.TrimSpace(e.DBName) == "" {
		return errors.New("DB_NAME is required")
	}
	if strings.TrimSpace(e.RedisAddr) == "" {
		return errors.New("REDIS_ADDR is required")
	}
	if e.RedisDB < 0 {
		return errors.New("REDIS_DB must not be negative")
	}
	if e.RedisPoolSize <= 0 {
		return errors.New("REDIS_POOL_SIZE must be positive")
	}
	if e.RedisMinIdleConns < 0 || e.RedisMinIdleConns > e.RedisPoolSize {
		return errors.New("REDIS_MIN_IDLE_CONNS must be between 0 and REDIS_POOL_SIZE")
	}
	if e.RedisDialTimeout <= 0 || e.RedisReadTimeout <= 0 || e.RedisWriteTimeout <= 0 {
		return errors.New("Redis timeouts must be positive")
	}
	if strings.TrimSpace(e.RedisAnalyticsStream) == "" {
		return errors.New("REDIS_ANALYTICS_STREAM is required")
	}
	if e.ClickEventsRetentionDays < 0 {
		return errors.New("CLICK_EVENTS_RETENTION_DAYS must not be negative")
	}
	if e.AnalyticsMaxQueryDays <= 0 {
		return errors.New("ANALYTICS_MAX_QUERY_DAYS must be positive")
	}
	return nil
}

func (e *Env) ValidateWorker() error {
	if e == nil {
		return errors.New("environment is required")
	}
	if strings.TrimSpace(e.DBName) == "" {
		return errors.New("DB_NAME is required")
	}
	if strings.TrimSpace(e.RedisAddr) == "" {
		return errors.New("REDIS_ADDR is required")
	}
	if e.RedisDB < 0 {
		return errors.New("REDIS_DB must not be negative")
	}
	if e.RedisPoolSize <= 0 {
		return errors.New("REDIS_POOL_SIZE must be positive")
	}
	if e.RedisMinIdleConns < 0 || e.RedisMinIdleConns > e.RedisPoolSize {
		return errors.New("REDIS_MIN_IDLE_CONNS must be between 0 and REDIS_POOL_SIZE")
	}
	if e.RedisDialTimeout <= 0 || e.RedisReadTimeout <= 0 || e.RedisWriteTimeout <= 0 {
		return errors.New("Redis timeouts must be positive")
	}
	if strings.TrimSpace(e.RedisAnalyticsStream) == "" {
		return errors.New("REDIS_ANALYTICS_STREAM is required")
	}
	if strings.TrimSpace(e.RedisAnalyticsConsumerGroup) == "" {
		return errors.New("REDIS_ANALYTICS_CONSUMER_GROUP is required")
	}
	if strings.TrimSpace(e.RedisAnalyticsConsumerName) == "" {
		return errors.New("REDIS_ANALYTICS_CONSUMER_NAME is required")
	}
	if e.AnalyticsBatchSize <= 0 {
		return errors.New("ANALYTICS_BATCH_SIZE must be positive")
	}
	if e.AnalyticsPollTimeout <= 0 {
		return errors.New("ANALYTICS_POLL_TIMEOUT must be positive")
	}
	if e.AnalyticsMaxRetries < 0 {
		return errors.New("ANALYTICS_MAX_RETRIES must not be negative")
	}
	if e.AnalyticsRetryBackoff < 0 {
		return errors.New("ANALYTICS_RETRY_BACKOFF must not be negative")
	}
	if e.AnalyticsPendingMinIdle < 0 {
		return errors.New("ANALYTICS_PENDING_MIN_IDLE must not be negative")
	}
	if e.AnalyticsProcessTimeout <= 0 {
		return errors.New("ANALYTICS_PROCESS_TIMEOUT must be positive")
	}
	if e.ClickEventsRetentionDays < 0 {
		return errors.New("CLICK_EVENTS_RETENTION_DAYS must not be negative")
	}
	if e.AnalyticsMaxQueryDays <= 0 {
		return errors.New("ANALYTICS_MAX_QUERY_DAYS must be positive")
	}
	return nil
}

func NewEnv() *Env {
	loadLocalEnv()

	env := &Env{
		AppEnv:        getEnv("APP_ENV", "development"),
		ServerAddress: getEnv("SERVER_ADDRESS", ":8080"),
		LogLevel:      getEnv("LOG_LEVEL", "info"),

		ContextTimeout:    getEnvAsInt("CONTEXT_TIMEOUT", 10),
		ShortenerDomains:  getEnvAsList("SHORTENER_DOMAINS", []string{"localhost"}),
		TrustedProxyCIDRs: getEnvAsList("TRUSTED_PROXY_CIDRS", nil),
		AnalyticsIPSalt:   getEnv("ANALYTICS_IP_SALT", ""),

		MongoURI:     getFirstNonEmpty("MONGODB_URI", "DATABASE_URL"),
		DBHost:       getEnv("DB_HOST", ""),
		DBPort:       getEnv("DB_PORT", ""),
		DBUser:       getEnv("DB_USER", ""),
		DBPass:       getEnv("DB_PASS", ""),
		DBName:       getEnv("DB_NAME", ""),
		DBAuthSource: getEnv("DB_AUTH_SOURCE", ""),

		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		RedisDB:           getEnvAsInt("REDIS_DB", 0),
		RedisPoolSize:     getEnvAsInt("REDIS_POOL_SIZE", 100),
		RedisMinIdleConns: getEnvAsInt("REDIS_MIN_IDLE_CONNS", 10),
		RedisDialTimeout:  getEnvAsDuration("REDIS_DIAL_TIMEOUT", 5*time.Second),
		RedisReadTimeout:  getEnvAsDuration("REDIS_READ_TIMEOUT", 3*time.Second),
		RedisWriteTimeout: getEnvAsDuration("REDIS_WRITE_TIMEOUT", 3*time.Second),

		RedisAnalyticsStream:        getEnv("REDIS_ANALYTICS_STREAM", "stream:clicks"),
		RedisAnalyticsConsumerGroup: getEnv("REDIS_ANALYTICS_CONSUMER_GROUP", "analytics-workers"),
		RedisAnalyticsConsumerName:  getEnv("REDIS_ANALYTICS_CONSUMER_NAME", ""),
		AnalyticsBatchSize:          getEnvAsInt("ANALYTICS_BATCH_SIZE", 100),
		AnalyticsPollTimeout:        getEnvAsDuration("ANALYTICS_POLL_TIMEOUT", 2*time.Second),
		AnalyticsMaxRetries:         getEnvAsInt("ANALYTICS_MAX_RETRIES", 3),
		AnalyticsRetryBackoff:       getEnvAsDuration("ANALYTICS_RETRY_BACKOFF", 500*time.Millisecond),
		AnalyticsPendingMinIdle:     getEnvAsDuration("ANALYTICS_PENDING_MIN_IDLE", 30*time.Second),
		AnalyticsProcessTimeout:     getEnvAsDuration("ANALYTICS_PROCESS_TIMEOUT", 30*time.Second),
		ClickEventsRetentionDays:    getEnvAsInt("CLICK_EVENTS_RETENTION_DAYS", 0),
		AnalyticsMaxQueryDays:       getEnvAsInt("ANALYTICS_MAX_QUERY_DAYS", 366),

		AccessTokenSecret:      getEnv("ACCESS_TOKEN_SECRET", ""),
		RefreshTokenSecret:     getEnv("REFRESH_TOKEN_SECRET", ""),
		AccessTokenExpiryHour:  getEnvAsInt("ACCESS_TOKEN_EXPIRY_HOUR", 2),
		RefreshTokenExpiryHour: getEnvAsInt("REFRESH_TOKEN_EXPIRY_HOUR", 168),
	}

	// Derive the database name from the URI path when only a connection string
	// is supplied.
	if strings.TrimSpace(env.DBName) == "" && strings.TrimSpace(env.MongoURI) != "" {
		if db := extractDBNameFromURI(env.MongoURI); db != "" {
			env.DBName = db
		}
	}

	// authSource defaults to the database name, falling back to "admin".
	// Note: MongoDB root users (as created by the docker-compose db service)
	// authenticate against "admin", so set DB_AUTH_SOURCE=admin explicitly there.
	if strings.TrimSpace(env.DBAuthSource) == "" {
		if strings.TrimSpace(env.DBName) != "" {
			env.DBAuthSource = env.DBName
		} else {
			env.DBAuthSource = "admin"
		}
	}

	if strings.EqualFold(env.AppEnv, "development") {
		log.Println("The app is running in development env")
	}

	return env
}

func (e *Env) ClickEventsRetention() time.Duration {
	if e == nil || e.ClickEventsRetentionDays <= 0 {
		return 0
	}
	return time.Duration(e.ClickEventsRetentionDays) * 24 * time.Hour
}

func loadLocalEnv() {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
		return
	}
	if err := godotenv.Load(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("warning: could not load .env file: %v", err)
		}
	}
}

func getEnv(key, defaultVal string) string {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return defaultVal
}

func getFirstNonEmpty(keys ...string) string {
	for _, key := range keys {
		if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func getEnvAsInt(key string, defaultVal int) int {
	valueStr, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(valueStr) == "" {
		return defaultVal
	}
	value, err := strconv.Atoi(strings.TrimSpace(valueStr))
	if err != nil {
		log.Fatalf("invalid value for %s: %v", key, err)
	}
	return value
}

func getEnvAsDuration(key string, defaultVal time.Duration) time.Duration {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return defaultVal
	}
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		log.Fatalf("invalid duration for %s: %v", key, err)
	}
	return duration
}

func getEnvAsList(key string, defaultVal []string) []string {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return append([]string(nil), defaultVal...)
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func extractDBNameFromURI(uri string) string {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return ""
	}
	path := strings.Trim(parsed.Path, "/")
	if path == "" {
		return ""
	}
	if idx := strings.Index(path, "/"); idx != -1 {
		path = path[:idx]
	}
	return path
}
