package telemetry

import "testing"

func TestOperationalTelemetrySchemaIsBoundedAndSafe(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	if len(Metrics) != 7 || len(Events) != 7 {
		t.Fatalf("coverage changed: metrics=%d events=%d", len(Metrics), len(Events))
	}
}
