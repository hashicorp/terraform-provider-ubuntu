package assets

import "fmt"

type Spec struct {
	ExecutorArches []string
	PluginModules  []string
}

type Asset struct {
	Bytes       []byte
	Digest      string
	Compression string
}

type Store interface {
	Validate() error
	ExecutorBinary(arch string) (Asset, error)
	PluginModule(name string) (Asset, error)
}

type MissingAssetsError struct {
	Executors []string
	Plugins   []string
}

func (e *MissingAssetsError) Error() string {
	items := make([]string, 0, len(e.Executors)+len(e.Plugins))
	for _, arch := range e.Executors {
		items = append(items, fmt.Sprintf("executor:%s", arch))
	}
	for _, name := range e.Plugins {
		items = append(items, fmt.Sprintf("plugin:%s", name))
	}
	return fmt.Sprintf("missing runtime assets: %s", joinItems(items))
}

func (e *MissingAssetsError) empty() bool {
	return len(e.Executors) == 0 && len(e.Plugins) == 0
}
