package schema

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hashicorp/boundary/internal/db/schema/postgres"
	"github.com/hashicorp/boundary/internal/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState(t *testing.T) {
	dialect := "postgres"
	ctx := context.Background()
	c, u, err := docker.StartDbInDocker(ctx, dialect)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, c())
	})
	d, err := sql.Open(dialect, u)
	require.NoError(t, err)

	m, err := NewManager(ctx, dialect, d)
	require.NoError(t, err)
	want := &State{
		BinarySchemaVersion: BinarySchemaVersion(dialect),
	}
	s, err := m.CurrentState(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, s)

	testDriver, err := postgres.NewPostgres(ctx, d)
	require.NoError(t, err)
	require.NoError(t, testDriver.SetVersion(ctx, 2, true))

	want = &State{
		InitializationStarted: true,
		BinarySchemaVersion:   BinarySchemaVersion(dialect),
		Dirty:                 true,
		CurrentSchemaVersion:  2,
	}
	s, err = m.CurrentState(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, s)
}
