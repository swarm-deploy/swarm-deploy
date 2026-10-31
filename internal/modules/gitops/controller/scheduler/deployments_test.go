package scheduler

import (
 "context"
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
 "github.com/swarm-deploy/swarm-deploy/internal/config"
 "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
 "go.uber.org/mock/gomock"
)

func TestDeploymentQueueKeepsLatestPendingRevision(t *testing.T) {
 dir := t.TempDir()
 source := filepath.Join(dir, "checkout")
 require.NoError(t, os.Mkdir(source, 0o700))
 file := filepath.Join(source, "app.yaml")
 require.NoError(t, os.WriteFile(file, []byte("first"), 0o600))
 ctrl := gomock.NewController(t)
 reconciler := stackloop.NewMockStackReconciler(ctrl)
 queue := NewDeploymentQueue(reconciler, dir)
 enqueue := func(commit string) {
  t.Helper()
  require.NoError(t, queue.Enqueue(t.Context(), stackloop.ReconciliationRequest{
   Stack: config.StackSpec{Name:"app",ComposeFile:"app.yaml"}, Commit:commit,
  }, source))
 }
 enqueue("a")
 require.NoError(t, os.WriteFile(file, []byte("second"), 0o600))
 enqueue("b")
 assert.Len(t, queue.pending, 1)
 job := queue.pending["app"]
 assert.Equal(t, "b", job.request.Commit)
 content, err := os.ReadFile(filepath.Join(job.folder, "app.yaml"))
 require.NoError(t, err)
 assert.Equal(t, "second", string(content))
 assert.Len(t, queue.order, 1)
 queue.Stop()
 assert.NoDirExists(t, job.folder)
}

func TestDeploymentQueueDoesNotBlockEnqueueOnActiveDeployment(t *testing.T) {
 dir := t.TempDir()
 source := filepath.Join(dir, "checkout")
 require.NoError(t, os.Mkdir(source, 0o700))
 require.NoError(t, os.WriteFile(filepath.Join(source, "app.yaml"), []byte("first"), 0o600))
 ctrl := gomock.NewController(t)
 reconciler := stackloop.NewMockStackReconciler(ctrl)
 started := make(chan struct{})
 release := make(chan struct{})
 reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
  func(_ context.Context, request stackloop.ReconciliationRequest) error {
   assert.Equal(t,"a",request.Commit)
   close(started)
   <-release
   return nil
  },
 )
 reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
  func(_ context.Context, request stackloop.ReconciliationRequest) error {
   assert.Equal(t,"b",request.Commit)
   return nil
  },
 )
 queue := NewDeploymentQueue(reconciler, dir)
 ctx,cancel := context.WithCancel(t.Context())
 defer cancel()
 done := make(chan struct{})
 go func(){defer close(done);queue.Run(ctx)}()
 require.NoError(t,queue.Enqueue(t.Context(),stackloop.ReconciliationRequest{
  Stack:config.StackSpec{Name:"app",ComposeFile:"app.yaml"},Commit:"a",
 },source))
 select {case <-started:case <-time.After(time.Second):close(release);require.FailNow(t,"first deploy not started")}
 require.NoError(t,os.WriteFile(filepath.Join(source,"app.yaml"),[]byte("second"),0o600))
 require.NoError(t,queue.Enqueue(t.Context(),stackloop.ReconciliationRequest{
  Stack:config.StackSpec{Name:"app",ComposeFile:"app.yaml"},Commit:"b",
 },source))
 close(release)
 require.Eventually(t,func()bool{
  queue.mu.Lock();defer queue.mu.Unlock()
  return len(queue.running)==0 && len(queue.order)==0
 },time.Second,time.Millisecond)
 queue.Stop()
 select {case <-done:case <-time.After(time.Second):t.Fatal("worker did not stop")}
}

func TestSnapshotCheckoutRejectsSymlink(t *testing.T) {
 dir:=t.TempDir()
 source:=filepath.Join(dir,"checkout")
 require.NoError(t,os.Mkdir(source,0o700))
 require.NoError(t,os.Symlink("/etc/passwd",filepath.Join(source,"link")))
 _,err:=snapshotCheckout(t.Context(),source,filepath.Join(dir,"snapshots"))
 require.ErrorContains(t,err,"symlink")
}
