package db

// Postgres persistence stubs

type DB struct{}

func Connect(dsn string) (*DB, error) { return &DB{}, nil }
