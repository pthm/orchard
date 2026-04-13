package config

import "os"

func ResolveDSN(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("ORCHARD_DATABASE_URL"); v != "" {
		return v
	}
	if v := os.Getenv("ORCHARD_DSN"); v != "" {
		return v
	}
	return "postgres://localhost:5432/orchard?sslmode=disable"
}
