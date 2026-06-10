package ormx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestClusterReaderRoundRobin(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replica1DB, _ := newStubDB()
	defer replica1DB.Close()
	replica2DB, _ := newStubDB()
	defer replica2DB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica1, err := OpenWithDB(context.Background(), replica1DB, WithName("replica-a"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica1: %v", err)
	}
	replica2, err := OpenWithDB(context.Background(), replica2DB, WithName("replica-b"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica2: %v", err)
	}

	cluster, err := NewCluster(primary, replica1, replica2)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	if got := cluster.Reader().Name(); got != "replica-a" {
		t.Fatalf("expected first reader replica-a, got %q", got)
	}
	if got := cluster.Reader().Name(); got != "replica-b" {
		t.Fatalf("expected second reader replica-b, got %q", got)
	}
	if got := cluster.Reader().Name(); got != "replica-a" {
		t.Fatalf("expected third reader replica-a, got %q", got)
	}
}

func TestClusterWithTxAndReadTx(t *testing.T) {
	primaryDB, primaryState := newStubDB()
	defer primaryDB.Close()
	replicaDB, replicaState := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	if txErr := cluster.WithTx(context.Background(), func(_ *gorm.DB) error { return nil }); txErr != nil {
		t.Fatalf("cluster WithTx() error = %v", txErr)
	}
	if got := primaryState.beginCount.Load(); got != 1 {
		t.Fatalf("expected primary begin count 1, got %d", got)
	}
	if got := primaryState.commitCount.Load(); got != 1 {
		t.Fatalf("expected primary commit count 1, got %d", got)
	}

	if txErr := cluster.WithReadTx(context.Background(), func(_ *gorm.DB) error { return nil }); txErr != nil {
		t.Fatalf("cluster WithReadTx() error = %v", txErr)
	}
	if got := replicaState.readOnlyCount.Load(); got != 1 {
		t.Fatalf("expected replica read-only begin count 1, got %d", got)
	}
	if got := replicaState.commitCount.Load(); got != 1 {
		t.Fatalf("expected replica commit count 1, got %d", got)
	}
}

func TestClusterWithReadTxRoutesToPrimaryOnWriteFlag(t *testing.T) {
	primaryDB, primaryState := newStubDB()
	defer primaryDB.Close()
	replicaDB, replicaState := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	ctx := ContextWithWriteFlag(context.Background())
	if txErr := cluster.WithReadTx(ctx, func(_ *gorm.DB) error { return nil }); txErr != nil {
		t.Fatalf("cluster WithReadTx() error = %v", txErr)
	}

	if got := primaryState.readOnlyCount.Load(); got != 1 {
		t.Fatalf("expected primary read-only begin count 1, got %d", got)
	}
	if got := replicaState.readOnlyCount.Load(); got != 0 {
		t.Fatalf("expected replica read-only begin count 0, got %d", got)
	}
}

func TestDrainAndRecoverReplica(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	if drainErr := cluster.DrainReplica("replica", errors.New("replication lag")); drainErr != nil {
		t.Fatalf("DrainReplica() error = %v", drainErr)
	}

	replicas := cluster.ReplicaNodes()
	if replicas[0].State() != NodeStateDraining {
		t.Fatalf("expected draining replica, got %q", replicas[0].State())
	}
	if got := cluster.Reader().Name(); got != "primary" {
		t.Fatalf("expected reads to fall back to primary, got %q", got)
	}

	if recoverErr := cluster.RecoverReplica(context.Background(), "replica"); recoverErr != nil {
		t.Fatalf("RecoverReplica() error = %v", recoverErr)
	}
	if replicas = cluster.ReplicaNodes(); replicas[0].State() != NodeStateReady {
		t.Fatalf("expected recovered replica ready, got %q", replicas[0].State())
	}
	if got := cluster.Reader().Name(); got != "replica" {
		t.Fatalf("expected reads to return to replica, got %q", got)
	}
}

func TestClusterHealthCheckDegradedWhenReplicaDown(t *testing.T) {
	primaryDB, primaryState := newStubDB()
	defer primaryDB.Close()
	replicaDB, replicaState := newStubDB(withStubPingError(errors.New("replica unavailable")))
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	report := cluster.HealthCheck(context.Background())
	if report.Status != HealthStatusDegraded {
		t.Fatalf("expected degraded cluster status, got %q", report.Status)
	}
	if len(report.Nodes) != 2 {
		t.Fatalf("expected 2 node reports, got %d", len(report.Nodes))
	}
	if got := primaryState.pingCount.Load(); got != 1 {
		t.Fatalf("expected primary health ping once, got %d", got)
	}
	if got := replicaState.pingCount.Load(); got != 1 {
		t.Fatalf("expected replica health ping once, got %d", got)
	}

	metrics := cluster.Metrics()
	if len(metrics) != 20 {
		t.Fatalf("expected 20 metric samples, got %d", len(metrics))
	}
}

func TestClusterRefreshUsesCustomProbe(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	probeErr := errors.New("replica lag")

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(
		context.Background(),
		replicaDB,
		WithName("replica"),
		WithStartupPing(false),
		WithSkipInitializeWithVersion(true),
		WithHealthProbe(func(context.Context, *Client, NodeRole) error {
			return probeErr
		}),
	)
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	report := cluster.Refresh(context.Background())
	if report.Status != HealthStatusDegraded {
		t.Fatalf("expected degraded status, got %q", report.Status)
	}

	replicas := cluster.ReplicaNodes()
	if len(replicas) != 1 {
		t.Fatalf("expected one replica, got %d", len(replicas))
	}
	if replicas[0].State() != NodeStateDown {
		t.Fatalf("expected replica down, got %q", replicas[0].State())
	}
	if !errors.Is(replicas[0].LastError(), probeErr) {
		t.Fatalf("expected probe error, got %v", replicas[0].LastError())
	}
}

func TestClusterRunHealthLoop(t *testing.T) {
	primaryDB, primaryState := newStubDB()
	defer primaryDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}

	cluster, err := NewCluster(primary)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- cluster.RunHealthLoop(ctx, 10*time.Millisecond)
	}()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if primaryState.pingCount.Load() >= 2 {
			cancel()
			select {
			case loopErr := <-done:
				if loopErr != nil {
					t.Fatalf("RunHealthLoop() error = %v", loopErr)
				}
				return
			case <-time.After(100 * time.Millisecond):
				t.Fatal("RunHealthLoop() did not exit after cancellation")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done
	t.Fatalf("expected health loop to ping at least twice, got %d", primaryState.pingCount.Load())
}

func TestClusterRefreshMarksPrimaryDownWithoutSwitchingTopology(t *testing.T) {
	primaryDB, _ := newStubDB(withStubPingError(errors.New("primary unavailable")))
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica-a"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	report := cluster.Refresh(context.Background())
	if report.Status != HealthStatusDown {
		t.Fatalf("expected down report, got %q", report.Status)
	}
	if got := cluster.PrimaryNode().Name(); got != "primary" {
		t.Fatalf("expected primary routing to stay on primary, got %q", got)
	}
	if cluster.PrimaryNode().State() != NodeStateDown {
		t.Fatalf("expected primary node state down, got %q", cluster.PrimaryNode().State())
	}
	if got := cluster.Reader().Name(); got != "replica-a" {
		t.Fatalf("expected reads to keep using healthy replica, got %q", got)
	}
}

func TestSwitchPrimaryUpdatesWriteRoutingOnlyAfterExplicitOperatorAction(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica-a"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	switched, err := cluster.SwitchPrimary(context.Background(), "replica-a")
	if err != nil {
		t.Fatalf("SwitchPrimary() error = %v", err)
	}
	if switched.Name() != "replica-a" {
		t.Fatalf("expected switched node replica-a, got %q", switched.Name())
	}
	if got := cluster.PrimaryNode().Name(); got != "replica-a" {
		t.Fatalf("expected current primary replica-a, got %q", got)
	}
	if got := cluster.WriteDB(); got == nil || cluster.Primary().Name() != "replica-a" {
		t.Fatalf("expected writes to route to replica-a after explicit switch")
	}

	replicas := cluster.ReplicaNodes()
	if len(replicas) != 1 || replicas[0].Name() != "primary" {
		t.Fatalf("expected previous primary to move into replica set")
	}
	if replicas[0].State() != NodeStateDraining {
		t.Fatalf("expected previous primary to drain until explicitly recovered, got %q", replicas[0].State())
	}
}

func TestSwitchPrimaryDoesNotInvalidatePreviouslyFetchedWriter(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica-a"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	oldWriter, err := cluster.WriteClient()
	if err != nil {
		t.Fatalf("WriteClient(before switch) error = %v", err)
	}
	if oldWriter.Name() != "primary" {
		t.Fatalf("expected cached writer primary, got %q", oldWriter.Name())
	}

	_, err = cluster.SwitchPrimary(context.Background(), "replica-a")
	if err != nil {
		t.Fatalf("SwitchPrimary() error = %v", err)
	}

	newWriter, err := cluster.WriteClient()
	if err != nil {
		t.Fatalf("WriteClient(after switch) error = %v", err)
	}
	if newWriter.Name() != "replica-a" {
		t.Fatalf("expected fresh writer replica-a, got %q", newWriter.Name())
	}

	if oldWriter.Name() != "primary" {
		t.Fatalf("expected previously fetched writer to remain primary, got %q", oldWriter.Name())
	}
	if oldWriter == newWriter {
		t.Fatal("expected caller to re-fetch writer after switch")
	}
}

func TestSwitchPrimaryReturnsPrimaryWhenConcurrentSwitchAlreadyWon(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	replicaDB, _ := newStubDB(withStubPingHook(func() {
		once.Do(func() {
			close(started)
			<-release
		})
	}))
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica-a"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	type switchResult struct {
		node Node
		err  error
	}
	resultCh := make(chan switchResult, 1)
	go func() {
		node, switchErr := cluster.SwitchPrimary(context.Background(), "replica-a")
		resultCh <- switchResult{node: node, err: switchErr}
	}()

	<-started

	// Simulate a concurrent winner that switches the primary while the
	// first goroutine is blocked in Ping.
	cluster.mu.Lock()
	concurrentCandidate := cluster.findReplicaLocked("replica-a")
	if concurrentCandidate == nil {
		cluster.mu.Unlock()
		t.Fatalf("expected replica-a to still be a replica before concurrent switch")
	}
	cluster.switchPrimaryLocked(concurrentCandidate, errors.New("concurrent switch"))
	cluster.mu.Unlock()

	close(release)

	result := <-resultCh

	// With epoch-based TOCTOU protection, the concurrent loser detects the
	// topology change and returns errTopologyChanged instead of silently
	// returning stale data.
	if result.err != nil && !errors.Is(result.err, errTopologyChanged) {
		t.Fatalf("expected errTopologyChanged or success, got %v", result.err)
	}

	// Regardless of which goroutine won, the final topology must be correct.
	if cluster.PrimaryNode().Name() != "replica-a" {
		t.Fatalf("expected cluster primary replica-a, got %q", cluster.PrimaryNode().Name())
	}
}

func TestMarkPrimaryDownStopsWritesWithoutAutomaticPromotion(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	if markErr := cluster.MarkPrimaryDown(errors.New("write timeout")); markErr != nil {
		t.Fatalf("MarkPrimaryDown() error = %v", markErr)
	}

	writer, err := cluster.WriteClient()
	if !errors.Is(err, errPrimaryUnavailable) {
		t.Fatalf("expected errPrimaryUnavailable, got %v", err)
	}
	if writer != nil {
		t.Fatalf("expected nil writer when primary is down")
	}
	if got := cluster.PrimaryNode().Name(); got != "primary" {
		t.Fatalf("expected topology to remain on primary, got %q", got)
	}
	if got := cluster.Reader().Name(); got != "replica" {
		t.Fatalf("expected reads to keep using replica, got %q", got)
	}
}

func TestClusterHealthCheckDownWhenPrimaryDown(t *testing.T) {
	primaryDB, _ := newStubDB(withStubPingError(errors.New("primary unavailable")))
	defer primaryDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}

	cluster, err := NewCluster(primary)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	report := cluster.HealthCheck(context.Background())
	if report.Status != HealthStatusDown {
		t.Fatalf("expected down cluster status, got %q", report.Status)
	}
	if report.Healthy() {
		t.Fatalf("expected unhealthy cluster report")
	}
}

func TestClusterHealthCheckAcceptsNilContext(t *testing.T) {
	primaryDB, primaryState := newStubDB()
	defer primaryDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}

	cluster, err := NewCluster(primary)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	var nilCtx context.Context

	report := cluster.HealthCheck(nilCtx)
	if report.Status != HealthStatusUp {
		t.Fatalf("expected up cluster status, got %q", report.Status)
	}
	if got := primaryState.pingCount.Load(); got != 1 {
		t.Fatalf("expected primary ping count 1, got %d", got)
	}
}

func TestClusterRefreshAcceptsNilContext(t *testing.T) {
	primaryDB, primaryState := newStubDB()
	defer primaryDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}

	cluster, err := NewCluster(primary)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	var nilCtx context.Context

	report := cluster.Refresh(nilCtx)
	if report.Status != HealthStatusUp {
		t.Fatalf("expected up cluster status, got %q", report.Status)
	}
	if got := primaryState.pingCount.Load(); got != 1 {
		t.Fatalf("expected primary ping count 1, got %d", got)
	}
}

func TestReaderClientWithoutFallbackReturnsError(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewClusterWithOptions(primary, []*Client{replica}, WithReadFallbackToPrimary(false))
	if err != nil {
		t.Fatalf("NewClusterWithOptions() error = %v", err)
	}

	if drainErr := cluster.DrainReplica("replica", nil); drainErr != nil {
		t.Fatalf("DrainReplica() error = %v", drainErr)
	}

	client, err := cluster.ReaderClient()
	if !errors.Is(err, errNoReadableNode) {
		t.Fatalf("expected errNoReadableNode, got %v", err)
	}
	if client != nil {
		t.Fatalf("expected nil reader client when no readable node")
	}
}

func TestReaderClientCtxRoutesToPrimaryOnWriteFlag(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB,
		WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	// Without write flag — reads go to replica.
	client, err := cluster.ReaderClientCtx(context.Background())
	if err != nil {
		t.Fatalf("ReaderClientCtx() error = %v", err)
	}
	if client.Name() != "replica" {
		t.Fatalf("expected replica without write flag, got %q", client.Name())
	}

	// With write flag — reads route to primary.
	ctx := ContextWithWriteFlag(context.Background())
	client, err = cluster.ReaderClientCtx(ctx)
	if err != nil {
		t.Fatalf("ReaderClientCtx(writeFlag) error = %v", err)
	}
	if client.Name() != "primary" {
		t.Fatalf("expected primary with write flag, got %q", client.Name())
	}

	// ReaderClient (no ctx) still routes to replica — backward compatible.
	client, err = cluster.ReaderClient()
	if err != nil {
		t.Fatalf("ReaderClient() error = %v", err)
	}
	if client.Name() != "replica" {
		t.Fatalf("expected replica from ReaderClient(), got %q", client.Name())
	}
}

func TestReaderClientCtxClearedWriteFlag(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB,
		WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	ctx := ContextClearWriteFlag(ContextWithWriteFlag(context.Background()))
	client, err := cluster.ReaderClientCtx(ctx)
	if err != nil {
		t.Fatalf("ReaderClientCtx(clear) error = %v", err)
	}
	if client.Name() != "replica" {
		t.Fatalf("expected replica after clear, got %q", client.Name())
	}
}

func TestReaderClientCtxWriteWindowExpires(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB,
		WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	ctx := ContextWithWriteWindow(context.Background(), 20*time.Millisecond)
	client, err := cluster.ReaderClientCtx(ctx)
	if err != nil {
		t.Fatalf("ReaderClientCtx(window) error = %v", err)
	}
	if client.Name() != "primary" {
		t.Fatalf("expected primary before window expires, got %q", client.Name())
	}

	time.Sleep(40 * time.Millisecond)

	client, err = cluster.ReaderClientCtx(ctx)
	if err != nil {
		t.Fatalf("ReaderClientCtx(expired window) error = %v", err)
	}
	if client.Name() != "replica" {
		t.Fatalf("expected replica after window expires, got %q", client.Name())
	}
}

func TestReaderClientCtxReturnsPrimaryUnavailableWhenWriteFlagSetAndPrimaryDown(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB,
		WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	if markErr := cluster.MarkPrimaryDown(errors.New("primary down")); markErr != nil {
		t.Fatalf("MarkPrimaryDown() error = %v", markErr)
	}

	client, readErr := cluster.ReaderClientCtx(ContextWithWriteFlag(context.Background()))
	if !errors.Is(readErr, errPrimaryUnavailable) {
		t.Fatalf("expected errPrimaryUnavailable, got %v", readErr)
	}
	if client != nil {
		t.Fatalf("expected nil client, got %v", client)
	}
}

func TestReaderClientCtxReturnsPrimaryUnavailableWhenWriteFlagSetAndPrimaryDraining(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB,
		WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB,
		WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	cluster.mu.Lock()
	cluster.primary.setState(NodeStateDraining, errors.New("manual drain"))
	cluster.mu.Unlock()

	client, readErr := cluster.ReaderClientCtx(ContextWithWriteFlag(context.Background()))
	if !errors.Is(readErr, errPrimaryUnavailable) {
		t.Fatalf("expected errPrimaryUnavailable, got %v", readErr)
	}
	if client != nil {
		t.Fatalf("expected nil client, got %v", client)
	}
}

func TestHasWriteFlagDefaultFalse(t *testing.T) {
	if HasWriteFlag(context.Background()) {
		t.Fatal("expected false for plain context")
	}
	ctx := ContextWithWriteFlag(context.Background())
	if !HasWriteFlag(ctx) {
		t.Fatal("expected true after ContextWithWriteFlag")
	}
}

func TestClusterOperationsAfterCloseReturnError(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()
	replicaDB, _ := newStubDB()
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	if closeErr := cluster.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	// Every public method should return errClusterClosed after Close().
	assertClosed := func(name string, got error) {
		t.Helper()
		if !errors.Is(got, errClusterClosed) {
			t.Fatalf("%s after close: expected errClusterClosed, got %v", name, got)
		}
	}

	_, writeErr := cluster.WriteClient()
	assertClosed("WriteClient", writeErr)

	_, readErr := cluster.ReaderClient()
	assertClosed("ReaderClient", readErr)

	assertClosed("DrainReplica", cluster.DrainReplica("replica", nil))
	assertClosed("MarkPrimaryDown", cluster.MarkPrimaryDown(nil))
	assertClosed("RecoverReplica", cluster.RecoverReplica(context.Background(), "replica"))

	_, switchErr := cluster.SwitchPrimary(context.Background(), "replica")
	assertClosed("SwitchPrimary", switchErr)

	// Double close should also return errClusterClosed.
	assertClosed("double Close", cluster.Close())
}

func TestRecoverReplicaReturnsClusterClosedWhenClosedDuringPing(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	replicaDB, _ := newStubDB(withStubPingHook(func() {
		once.Do(func() {
			close(started)
			<-release
		})
	}))
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- cluster.RecoverReplica(context.Background(), "replica")
	}()

	<-started
	if closeErr := cluster.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	close(release)

	recoverErr := <-resultCh
	if !errors.Is(recoverErr, errClusterClosed) {
		t.Fatalf("expected errClusterClosed, got %v", recoverErr)
	}
}

func TestSwitchPrimaryReturnsClusterClosedWhenClosedDuringPing(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	replicaDB, _ := newStubDB(withStubPingHook(func() {
		once.Do(func() {
			close(started)
			<-release
		})
	}))
	defer replicaDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	replica, err := OpenWithDB(context.Background(), replicaDB, WithName("replica-a"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}

	cluster, err := NewCluster(primary, replica)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	type switchResult struct {
		node Node
		err  error
	}
	resultCh := make(chan switchResult, 1)
	go func() {
		node, switchErr := cluster.SwitchPrimary(context.Background(), "replica-a")
		resultCh <- switchResult{node: node, err: switchErr}
	}()

	<-started
	if closeErr := cluster.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	close(release)

	result := <-resultCh
	if !errors.Is(result.err, errClusterClosed) {
		t.Fatalf("expected errClusterClosed, got %v", result.err)
	}
}

func TestSwitchPrimaryFastPathReturnsClusterClosedWhenClosedDuringPingError(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	primaryDB, _ := newStubDB(
		withStubPingError(errors.New("primary unavailable")),
		withStubPingHook(func() {
			once.Do(func() {
				close(started)
				<-release
			})
		}),
	)
	defer primaryDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}

	cluster, err := NewCluster(primary)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	type switchResult struct {
		node Node
		err  error
	}
	resultCh := make(chan switchResult, 1)
	go func() {
		node, switchErr := cluster.SwitchPrimary(context.Background(), "primary")
		resultCh <- switchResult{node: node, err: switchErr}
	}()

	<-started
	if closeErr := cluster.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	close(release)

	result := <-resultCh
	if !errors.Is(result.err, errClusterClosed) {
		t.Fatalf("expected errClusterClosed, got %v", result.err)
	}
}

func TestSwitchPrimaryFastPathReturnsClusterClosedWhenClosedAfterSuccessfulPing(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	primaryDB, _ := newStubDB(withStubPingHook(func() {
		once.Do(func() {
			close(started)
			<-release
		})
	}))
	defer primaryDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}

	cluster, err := NewCluster(primary)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	type switchResult struct {
		node Node
		err  error
	}
	resultCh := make(chan switchResult, 1)
	go func() {
		node, switchErr := cluster.SwitchPrimary(context.Background(), "primary")
		resultCh <- switchResult{node: node, err: switchErr}
	}()

	<-started
	if closeErr := cluster.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	close(release)

	result := <-resultCh
	if !errors.Is(result.err, errClusterClosed) {
		t.Fatalf("expected errClusterClosed, got %v", result.err)
	}
}

func TestClusterWithTxAfterCloseReturnError(t *testing.T) {
	primaryDB, _ := newStubDB()
	defer primaryDB.Close()

	primary, err := OpenWithDB(context.Background(), primaryDB, WithName("primary"), WithStartupPing(false), WithSkipInitializeWithVersion(true))
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}

	cluster, err := NewCluster(primary)
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}

	_ = cluster.Close()

	txErr := cluster.WithTx(context.Background(), func(_ *gorm.DB) error { return nil })
	if !errors.Is(txErr, errClusterClosed) {
		t.Fatalf("WithTx after close: expected errClusterClosed, got %v", txErr)
	}
}

func TestConfigStringRedactsPassword(t *testing.T) {
	cfg := NewConfig(
		WithHost("10.0.0.1"),
		WithPort("3306"),
		WithUser("admin"),
		WithPassword("s3cret!"),
		WithDatabase("prod"),
	)

	str := cfg.String()
	if str == "" {
		t.Fatal("expected non-empty String()")
	}
	if contains(str, "s3cret!") {
		t.Fatalf("String() must not contain plaintext password, got: %s", str)
	}
	if !contains(str, "******") {
		t.Fatalf("String() should contain redacted password marker, got: %s", str)
	}

	goStr := cfg.GoString()
	if contains(goStr, "s3cret!") {
		t.Fatalf("GoString() must not contain plaintext password, got: %s", goStr)
	}
}

func TestDefaultConfigHasPoolAndTimeoutDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Pool.MaxOpenConns != 50 {
		t.Fatalf("expected default MaxOpenConns=50, got %d", cfg.Pool.MaxOpenConns)
	}
	if cfg.Pool.MaxIdleConns != 10 {
		t.Fatalf("expected default MaxIdleConns=10, got %d", cfg.Pool.MaxIdleConns)
	}
	if cfg.MySQL.ReadTimeout == 0 {
		t.Fatal("expected non-zero default ReadTimeout")
	}
	if cfg.MySQL.WriteTimeout == 0 {
		t.Fatal("expected non-zero default WriteTimeout")
	}
	if !cfg.Pool.hasMaxOpenConns || !cfg.Pool.hasMaxIdleConns {
		t.Fatal("expected pool has* flags to be true in defaults")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
