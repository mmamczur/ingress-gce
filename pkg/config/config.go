package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

type DynamicFlag string

// RegisterBool is called to define a new config flag
func (dc *DynamicConfig) RegisterBool(flag DynamicFlag, defaultValue bool) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	dc.defaultValues[flag] = defaultValue
	dc.registry[flag] = defaultValue
}

type DynamicConfig struct {
	configPath    string
	period        time.Duration
	defaultValues map[DynamicFlag]bool
	mu            sync.RWMutex
	registry      map[DynamicFlag]bool
}

func NewDynamicConfig(configPath string, period time.Duration) *DynamicConfig {
	// Initialize with defaults

	return &DynamicConfig{
		configPath:    configPath,
		period:        period,
		defaultValues: make(map[DynamicFlag]bool),
		registry:      make(map[DynamicFlag]bool),
	}
}

func (dc *DynamicConfig) Start() {
	go func() {
		ticker := time.NewTicker(dc.period)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				dc.reload()
			}
		}
	}()
}

func (dc *DynamicConfig) reload() {
	newValues := make(map[DynamicFlag]bool)

	for flag, defaultValue := range dc.defaultValues {
		// Start with default
		val := defaultValue

		filename := filepath.Join(dc.configPath, string(flag))
		content, err := os.ReadFile(filename)
		if err == nil {
			trimmed := strings.TrimSpace(string(content))
			boolVal, parseErr := strconv.ParseBool(trimmed)
			if parseErr == nil {
				val = boolVal
			} else {
				klog.Errorf("Failed to parse bool for flag %s from file %s: %v", flag, filename, parseErr)
			}
		} else if !os.IsNotExist(err) {
			klog.Errorf("Failed to read config file for flag %s: %v", flag, err)
		}
		newValues[flag] = val
	}

	dc.mu.Lock()
	dc.registry = newValues
	dc.mu.Unlock()
}

type DynamicConfigView struct {
	parent *DynamicConfig
	values map[DynamicFlag]bool
}

// GetSnapshotView returns the config that can be using during service processing (sync)
func (dc *DynamicConfig) GetSnapshotView() *DynamicConfigView {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	snapshot := make(map[DynamicFlag]bool, len(dc.registry))
	for k, v := range dc.registry {
		snapshot[k] = v
	}
	return &DynamicConfigView{parent: dc, values: snapshot}
}

func (v *DynamicConfigView) GetBool(flag DynamicFlag) bool {
	if val, ok := v.values[flag]; ok {
		return val
	}
	return v.parent.defaultValues[flag]
}
