package spike

import (
 "context"
 "database/sql"
 "errors"
 "path/filepath"
 "testing"

 stdlib "github.com/Thiht/transactor/stdlib"
 _ "modernc.org/sqlite"
)

func setup(t *testing.T) (*sql.DB, interface{ WithinTransaction(context.Context,func(context.Context) error) error },stdlib.DBGetter) {
 t.Helper()
 db,err:=sql.Open("sqlite",filepath.Join(t.TempDir(),"test.sqlite"))
 if err!=nil {t.Fatal(err)}
 t.Cleanup(func(){_ = db.Close()})
 db.SetMaxOpenConns(1)
 if _,err=db.Exec("CREATE TABLE entries (id TEXT PRIMARY KEY)");err!=nil {t.Fatal(err)}
 tr,getter:=stdlib.NewTransactor(db,stdlib.NestedTransactionsSavepoints)
 return db,tr,getter
}

func count(t *testing.T,db *sql.DB) int {
 t.Helper()
 var n int
 if err:=db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&n);err!=nil {t.Fatal(err)}
 return n
}

func TestCommitAndRollback(t *testing.T){
 db,tr,get:=setup(t)
 insert:=func(ctx context.Context,id string) error {
  _,err:=get(ctx).ExecContext(ctx,"INSERT INTO entries(id) VALUES(?)",id)
  return err
 }
 err:=tr.WithinTransaction(context.Background(),func(ctx context.Context) error {
  if err:=insert(ctx,"ok");err!=nil{return err}
  return nil
 })
 if err!=nil {t.Fatal(err)}
 if count(t,db)!=1 {t.Fatalf("expected 1 row after commit")}
 sentinel:=errors.New("rollback requested")
 err=tr.WithinTransaction(context.Background(),func(ctx context.Context) error {
  if err:=insert(ctx,"rollback");err!=nil{return err}
  return sentinel
 })
 if !errors.Is(err,sentinel){t.Fatalf("expected rollback error: %v",err)}
 if count(t,db)!=1{t.Fatalf("rollback lost: %d",count(t,db))}
}

func TestNestedSavepointRollbackDoesNotRollBackOuter(t *testing.T){
 db,tr,get:=setup(t)
 sentinel:=errors.New("inner rollback")
 err:=tr.WithinTransaction(context.Background(),func(ctx context.Context) error {
  if _,err:=get(ctx).ExecContext(ctx,"INSERT INTO entries(id) VALUES('outer')");err!=nil{return err}
  innerErr:=tr.WithinTransaction(ctx,func(ctx context.Context) error {
   if _,err:=get(ctx).ExecContext(ctx,"INSERT INTO entries(id) VALUES('inner')");err!=nil{return err}
   return sentinel
  })
  if !errors.Is(innerErr,sentinel){t.Fatalf("expected inner rollback: %v",innerErr)}
  return nil
 })
 if err!=nil {t.Fatal(err)}
 if count(t,db)!=1 {t.Fatalf("nested savepoint semantics unexpected: %d",count(t,db))}
}

func TestNestedCommitRollsBackWithOuter(t *testing.T){
 db,tr,get:=setup(t)
 sentinel:=errors.New("outer rollback")
 err:=tr.WithinTransaction(context.Background(),func(ctx context.Context) error {
  if err:=tr.WithinTransaction(ctx,func(inner context.Context) error {
   _,err:=get(inner).ExecContext(inner,"INSERT INTO entries(id) VALUES('inner-committed')")
   return err
  });err!=nil{return err}
  return sentinel
 })
 if !errors.Is(err,sentinel){t.Fatalf("want outer rollback: %v",err)}
 if count(t,db)!=0{t.Fatalf("inner changes escaped outer rollback")}
}

func TestContextPropagationUsesTx(t *testing.T){
 _,tr,get:=setup(t)
 err:=tr.WithinTransaction(context.Background(),func(ctx context.Context) error {
  if !stdlib.IsWithinTransaction(ctx){t.Fatal("context not marked transactional")}
  if _,ok:=get(ctx).(*sql.Tx);!ok{t.Fatalf("DB getter did not return sql.Tx")}
  return nil
 })
 if err!=nil {t.Fatal(err)}
}
