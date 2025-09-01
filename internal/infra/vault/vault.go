package vault

// Stub vault interface and local AES-256 cache placeholder

type SecretStore interface {
	Get(key string) (string, error)
}

type DevEnvStore struct{}

func (d DevEnvStore) Get(key string) (string, error) { return "", nil }
