package collector

import (
	"reflect"
	"testing"
)

func TestPartialConfigurationPreservesLocalSelections(t *testing.T) {
	c := New(Config{Interfaces: []string{"eth0"}, Mounts: []string{"/data"}, HostRoot: "/host"})
	hostRoot := c.config.HostRoot
	c.UpdateConfig(Config{})
	if !reflect.DeepEqual(c.config.Interfaces, []string{"eth0"}) || !reflect.DeepEqual(c.config.Mounts, []string{"/data"}) {
		t.Fatal("omitted fields erased local selections")
	}
	c.UpdateConfig(Config{Interfaces: []string{}})
	if len(c.config.Interfaces) != 0 || !reflect.DeepEqual(c.config.Mounts, []string{"/data"}) {
		t.Fatal("explicit reset did not preserve other field")
	}
	if c.config.HostRoot != hostRoot {
		t.Fatal("host root changed")
	}
}
