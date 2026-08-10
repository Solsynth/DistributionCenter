package integration

import "testing"

func TestDialRequiresTarget(t *testing.T) {
	if _, err := Dial("", false, false); err == nil {
		t.Fatal("Dial() error = nil, want required target error")
	}
}

func TestDialBuildsInsecureAndTLSConnections(t *testing.T) {
	insecureConn, err := Dial("dns:///develop:9090", false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer insecureConn.Close()
	if insecureConn.Target() != "dns:///develop:9090" {
		t.Fatalf("insecure target = %q", insecureConn.Target())
	}
	tlsConn, err := Dial("dns:///develop:9091", true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer tlsConn.Close()
	if tlsConn.Target() != "dns:///develop:9091" {
		t.Fatalf("tls target = %q", tlsConn.Target())
	}
}
