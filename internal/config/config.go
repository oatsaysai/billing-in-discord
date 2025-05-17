package config

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	DiscordBot DiscordBotConfig
	Firebase   FirebaseConfig
	PostgreSQL PostgreSQLConfig
	OCR        OCRConfig
	Server     ServerConfig
}

// DiscordBotConfig holds Discord bot configuration
type DiscordBotConfig struct {
	Token string
}

// FirebaseConfig holds Firebase configuration
type FirebaseConfig struct {
	MainProjectID         string
	ServiceAccountKeyPath string
	SiteNamePrefix        string
	CliPath               string
	WebhookURL            string
}

// PostgreSQLConfig holds database configuration
type PostgreSQLConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	DBName       string
	Schema       string
	PoolMaxConns int
}

// OCRConfig holds OCR service configuration
type OCRConfig struct {
	ApiUrl string
	ApiKey string
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Port string
}

// Load loads configuration from file and environment variables
func Load(configPath string) (*Config, error) {
	// Set default locations for config file
	if configPath == "" {
		configPath = "config.yaml"
	}

	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")

	// Load configuration
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Unmarshal config into struct
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate required fields
	if cfg.DiscordBot.Token == "" {
		return nil, fmt.Errorf("discord bot token is required")
	}

	if cfg.PostgreSQL.Host == "" || cfg.PostgreSQL.DBName == "" {
		return nil, fmt.Errorf("database configuration is incomplete")
	}

	// Get absolute path for service account key if provided
	if cfg.Firebase.ServiceAccountKeyPath != "" {
		absPath, err := filepath.Abs(cfg.Firebase.ServiceAccountKeyPath)
		if err != nil {
			log.Printf("Warning: Could not resolve absolute path for Firebase service account key: %v", err)
		} else {
			cfg.Firebase.ServiceAccountKeyPath = absPath
		}
	}

	return &cfg, nil
}

// Initialize sets up viper with defaults and loads config
func Initialize() {
	// Set default locations for config file
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	// Set defaults
	viper.SetDefault("PostgreSQL.Host", "localhost")
	viper.SetDefault("PostgreSQL.Port", 5432)
	viper.SetDefault("PostgreSQL.User", "postgres")
	viper.SetDefault("PostgreSQL.DBName", "billing-db")
	viper.SetDefault("PostgreSQL.Schema", "public")
	viper.SetDefault("PostgreSQL.PoolMaxConns", 10)

	viper.SetDefault("Firebase.CliPath", "firebase")
	viper.SetDefault("Firebase.SiteNamePrefix", "psweb")
	viper.SetDefault("Firebase.WebhookURL", "/api/bill-webhook")

	viper.SetDefault("Server.Port", "8080")

	// Load configuration
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Fatal error reading config file: %v", err)
	}

	log.Println("Configuration loaded successfully")
}

// GetString gets a string value from the configuration
func GetString(key string) string {
	return viper.GetString(key)
}

// GetInt gets an integer value from the configuration
func GetInt(key string) int {
	return viper.GetInt(key)
}

// GetBool gets a boolean value from the configuration
func GetBool(key string) bool {
	return viper.GetBool(key)
}

// GetFloat64 gets a float64 value from the configuration
func GetFloat64(key string) float64 {
	return viper.GetFloat64(key)
}
