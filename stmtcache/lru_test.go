package stmtcache_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/ethanpailes/pgconn"
	"github.com/ethanpailes/pgconn/stmtcache"

	"github.com/stretchr/testify/require"
)

func TestLRUModePrepare(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	conn, err := pgconn.Connect(ctx, os.Getenv("PGX_TEST_CONN_STRING"))
	require.NoError(t, err)
	defer conn.Close(ctx)

	cache := stmtcache.NewLRU(conn, stmtcache.ModePrepare, 2)
	require.EqualValues(t, 0, cache.Len())
	require.EqualValues(t, 2, cache.Cap())
	require.EqualValues(t, stmtcache.ModePrepare, cache.Mode())

	psd, err := cache.Get(ctx, "select 1")
	require.NoError(t, err)
	require.NotNil(t, psd)
	require.EqualValues(t, 1, cache.Len())
	require.ElementsMatch(t, []string{"select 1"}, fetchServerStatements(t, ctx, conn))

	psd, err = cache.Get(ctx, "select 1")
	require.NoError(t, err)
	require.NotNil(t, psd)
	require.EqualValues(t, 1, cache.Len())
	require.ElementsMatch(t, []string{"select 1"}, fetchServerStatements(t, ctx, conn))

	psd, err = cache.Get(ctx, "select 2")
	require.NoError(t, err)
	require.NotNil(t, psd)
	require.EqualValues(t, 2, cache.Len())
	require.ElementsMatch(t, []string{"select 1", "select 2"}, fetchServerStatements(t, ctx, conn))

	psd, err = cache.Get(ctx, "select 3")
	require.NoError(t, err)
	require.NotNil(t, psd)
	require.EqualValues(t, 2, cache.Len())
	require.ElementsMatch(t, []string{"select 2", "select 3"}, fetchServerStatements(t, ctx, conn))

	err = cache.Clear(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, cache.Len())
	require.Empty(t, fetchServerStatements(t, ctx, conn))
}

func TestLRUStmtInvalidation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	conn, err := pgconn.Connect(ctx, os.Getenv("PGX_TEST_CONN_STRING"))
	require.NoError(t, err)
	defer conn.Close(ctx)

	// we construct a fake error because its not super straightforward to actually call
	// a prepared statement from the LRU cache without the helper routines which live
	// in pgx proper.
	fakeInvalidCachePlanError := &pgconn.PgError{
		Severity: "ERROR",
		Code:     "0A000",
		Message:  "cached plan must not change result type",
	}

	cache := stmtcache.NewLRU(conn, stmtcache.ModePrepare, 2)

	//
	// outside of a transaction, we eagerly flush the statement
	//

	_, err = cache.Get(ctx, "select 1")
	require.NoError(t, err)
	require.EqualValues(t, 1, cache.Len())
	require.ElementsMatch(t, []string{"select 1"}, fetchServerStatements(t, ctx, conn))

	err = cache.StatementErrored(ctx, "select 1", fakeInvalidCachePlanError)
	require.NoError(t, err)
	require.EqualValues(t, 0, cache.Len())
	require.ElementsMatch(t, []string{}, fetchServerStatements(t, ctx, conn))

	//
	// within an errored transaction, we defer the flush to after the first get
	// that happens after the transaction is rolled back
	//

	_, err = cache.Get(ctx, "select 1")
	require.NoError(t, err)
	require.EqualValues(t, 1, cache.Len())
	require.ElementsMatch(t, []string{"select 1"}, fetchServerStatements(t, ctx, conn))

	res := conn.Exec(ctx, "begin")
	require.NoError(t, res.Close())
	require.Equal(t, byte('T'), conn.TxStatus())

	res = conn.Exec(ctx, "selec")
	require.Error(t, res.Close())
	require.Equal(t, byte('E'), conn.TxStatus())

	err = cache.StatementErrored(ctx, "select 1", fakeInvalidCachePlanError)
	require.EqualValues(t, 1, cache.Len())

	res = conn.Exec(ctx, "rollback")
	require.NoError(t, res.Close())

	_, err = cache.Get(ctx, "select 2")
	require.EqualValues(t, 1, cache.Len())
	require.ElementsMatch(t, []string{"select 2"}, fetchServerStatements(t, ctx, conn))
}

func TestLRUModePrepareStress(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	conn, err := pgconn.Connect(ctx, os.Getenv("PGX_TEST_CONN_STRING"))
	require.NoError(t, err)
	defer conn.Close(ctx)

	cache := stmtcache.NewLRU(conn, stmtcache.ModePrepare, 8)
	require.EqualValues(t, 0, cache.Len())
	require.EqualValues(t, 8, cache.Cap())
	require.EqualValues(t, stmtcache.ModePrepare, cache.Mode())

	for i := 0; i < 1000; i++ {
		psd, err := cache.Get(ctx, fmt.Sprintf("select %d", rand.Intn(50)))
		require.NoError(t, err)
		require.NotNil(t, psd)
		result := conn.ExecPrepared(ctx, psd.Name, nil, nil, nil).Read()
		require.NoError(t, result.Err)
	}
}

func TestLRUModeDescribe(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	conn, err := pgconn.Connect(ctx, os.Getenv("PGX_TEST_CONN_STRING"))
	require.NoError(t, err)
	defer conn.Close(ctx)

	cache := stmtcache.NewLRU(conn, stmtcache.ModeDescribe, 2)
	require.EqualValues(t, 0, cache.Len())
	require.EqualValues(t, 2, cache.Cap())
	require.EqualValues(t, stmtcache.ModeDescribe, cache.Mode())

	psd, err := cache.Get(ctx, "select 1")
	require.NoError(t, err)
	require.NotNil(t, psd)
	require.EqualValues(t, 1, cache.Len())
	require.Empty(t, fetchServerStatements(t, ctx, conn))

	psd, err = cache.Get(ctx, "select 1")
	require.NoError(t, err)
	require.NotNil(t, psd)
	require.EqualValues(t, 1, cache.Len())
	require.Empty(t, fetchServerStatements(t, ctx, conn))

	psd, err = cache.Get(ctx, "select 2")
	require.NoError(t, err)
	require.NotNil(t, psd)
	require.EqualValues(t, 2, cache.Len())
	require.Empty(t, fetchServerStatements(t, ctx, conn))

	psd, err = cache.Get(ctx, "select 3")
	require.NoError(t, err)
	require.NotNil(t, psd)
	require.EqualValues(t, 2, cache.Len())
	require.Empty(t, fetchServerStatements(t, ctx, conn))

	err = cache.Clear(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, cache.Len())
	require.Empty(t, fetchServerStatements(t, ctx, conn))
}

func fetchServerStatements(t testing.TB, ctx context.Context, conn *pgconn.PgConn) []string {
	result := conn.ExecParams(ctx, `select statement from pg_prepared_statements`, nil, nil, nil, nil).Read()
	require.NoError(t, result.Err)
	var statements []string
	for _, r := range result.Rows {
		statements = append(statements, string(r[0]))
	}
	return statements
}
