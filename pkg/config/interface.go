package config

type ExperimentsProvider interface {
	Init()
	GetBool(name string) bool
}
