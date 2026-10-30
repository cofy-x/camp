package seatbelt

import "testing"

func TestControlEndpointsNeverGrantOtherOrigins(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:1", "https://127.0.0.1:1", "http://127.0.0.1", "http://127.0.0.1:1/path", "http://user@127.0.0.1:1", "http://127.0.0.1:1?key=secret", "http://192.0.2.1:1"} {
		if _, err := ControlPorts(endpoint); err == nil {
			t.Fatalf("overbroad control origin: %s", endpoint)
		}
	}
	ports, err := ControlPorts("http://127.0.0.1:2345", "", "http://127.0.0.1:3456")
	if err != nil || len(ports) != 2 || ports[0] != 2345 || ports[1] != 3456 {
		t.Fatalf("control ports: %v %v", ports, err)
	}
}
