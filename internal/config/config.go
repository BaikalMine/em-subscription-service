package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config содержит параметры окружения, необходимые сервису подписок.
type Config struct {
	ServerPort string
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string
	LogLevel   string
}

// Load читает переменные окружения (с .env при наличии) и формирует конфигурацию.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		ServerPort: getEnv("SERVER_PORT", "8080"),
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", "postgres"),
		DBName:     getEnv("DB_NAME", "subscriptions"),
		DBSSLMode:  getEnv("DB_SSL_MODE", "disable"),
		LogLevel:   getEnv("LOG_LEVEL", "info"),
	}

	return cfg, nil
}

// DSN собирает строку подключения к Postgres на основе конфигурации.
func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode)
}

func getEnv(key, fallback string) string {
	// Возвращаем переменную окружения, если задана, иначе дефолт.
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
