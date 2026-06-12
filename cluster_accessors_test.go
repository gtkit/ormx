package ormx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// newStubClient 用 stub driver 打开一个不连网的 Client。
func newStubClient(t *testing.T, name string) *Client {
	t.Helper()
	db, _ := newStubDB()
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(context.Background(), db, WithName(name), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("OpenWithDB(%s): %v", name, err)
	}
	return client
}

func TestClientConfig(t *testing.T) {
	client := newStubClient(t, "primary")
	if got := client.Config().Name; got != "primary" {
		t.Fatalf("Config().Name = %q, want primary", got)
	}
}

func TestNodeAccessors(t *testing.T) {
	primary := newStubClient(t, "primary")
	replica := newStubClient(t, "replica-a")

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster: %v", err)
	}

	node := cluster.PrimaryNode()
	if node.Name() != "primary" {
		t.Fatalf("Name = %q", node.Name())
	}
	if node.Role() != RolePrimary {
		t.Fatalf("Role = %q", node.Role())
	}
	if node.Client() != primary {
		t.Fatal("expected primary client")
	}
	if node.State() != NodeStateReady {
		t.Fatalf("State = %q", node.State())
	}
	if !node.Healthy() {
		t.Fatal("expected primary node healthy")
	}
	if node.LastError() != nil {
		t.Fatalf("LastError = %v", node.LastError())
	}
	if node.UpdatedAt().IsZero() {
		t.Fatal("expected non-zero UpdatedAt")
	}

	cause := errors.New("maintenance")
	if drainErr := cluster.DrainReplica("replica-a", cause); drainErr != nil {
		t.Fatalf("DrainReplica: %v", drainErr)
	}
	drained := cluster.ReplicaNodes()[0]
	if drained.Healthy() {
		t.Fatal("expected drained replica unhealthy")
	}
	if drained.State() != NodeStateDraining {
		t.Fatalf("State = %q", drained.State())
	}
	if !errors.Is(drained.LastError(), cause) {
		t.Fatalf("LastError = %v, want %v", drained.LastError(), cause)
	}
}

func TestClusterDeprecatedDBGetters(t *testing.T) {
	primary := newStubClient(t, "primary")
	replica := newStubClient(t, "replica-a")

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster: %v", err)
	}

	if db := cluster.ReadDB(); db == nil {
		t.Fatal("expected ReadDB to return replica db")
	}
	writeCtx := ContextWithWriteFlag(context.Background())
	if db := cluster.ReadDBCtx(writeCtx); db != primary.DB() {
		t.Fatal("expected write-flag reads to route to primary")
	}
	if db := cluster.MustWriteDB(); db != primary.DB() {
		t.Fatal("expected MustWriteDB to return primary db")
	}
	if db := cluster.MustReadDB(); db == nil {
		t.Fatal("expected MustReadDB to return replica db")
	}

	if closeErr := cluster.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	if db := cluster.ReadDB(); db != nil {
		t.Fatal("expected nil ReadDB after close")
	}
	if db := cluster.ReadDBCtx(writeCtx); db != nil {
		t.Fatal("expected nil ReadDBCtx after close")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected MustWriteDB to panic after close")
		}
	}()
	_ = cluster.MustWriteDB()
}

func TestMustReadDBPanicsWhenNoReadableNode(t *testing.T) {
	primary := newStubClient(t, "primary")
	cluster, err := NewClusterWithOptions(primary, nil, WithReadFallbackToPrimary(false))
	if err != nil {
		t.Fatalf("NewClusterWithOptions: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected MustReadDB to panic")
		}
	}()
	_ = cluster.MustReadDB()
}

func TestClusterOptionSetters(t *testing.T) {
	opts := defaultClusterOptions()

	WithAutoRecoverReplicas(false)(&opts)
	if opts.autoRecoverReplicas {
		t.Fatal("expected autoRecoverReplicas false")
	}

	WithHealthCheckTimeout(2 * time.Second)(&opts)
	if opts.healthCheckTimeout != 2*time.Second {
		t.Fatalf("healthCheckTimeout = %v", opts.healthCheckTimeout)
	}

	WithHealthCheckTimeout(0)(&opts) // 非正值被忽略
	if opts.healthCheckTimeout != 2*time.Second {
		t.Fatalf("expected non-positive timeout ignored, got %v", opts.healthCheckTimeout)
	}
}

func TestOpenClusterPrimaryConfigError(t *testing.T) {
	cluster, err := OpenCluster(context.Background(), Config{})
	if err == nil {
		t.Fatal("expected error for config without address")
	}
	if cluster != nil {
		t.Fatalf("expected nil cluster, got %v", cluster)
	}
}

func TestMustOpenPanicsOnInvalidConfig(t *testing.T) {
	t.Run("Config.MustOpen", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = Config{}.MustOpen(context.Background())
	})

	t.Run("包级 MustOpen", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = MustOpen(context.Background(), WithHost(""), WithPort(""))
	})
}

func TestStateToHealthStatus(t *testing.T) {
	tests := []struct {
		state NodeState
		want  HealthStatus
	}{
		{NodeStateReady, HealthStatusUp},
		{NodeStateDraining, HealthStatusDegraded},
		{NodeStateDown, HealthStatusDown},
		{NodeState("unknown"), HealthStatusDown},
	}
	for _, tt := range tests {
		if got := stateToHealthStatus(tt.state); got != tt.want {
			t.Fatalf("stateToHealthStatus(%q) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestHealthReportHealthy(t *testing.T) {
	if !(HealthReport{Status: HealthStatusUp}).Healthy() {
		t.Fatal("expected up report healthy")
	}
	if (HealthReport{Status: HealthStatusDown}).Healthy() {
		t.Fatal("expected down report unhealthy")
	}
}
