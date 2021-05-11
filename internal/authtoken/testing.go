package authtoken

import (
	"context"
	"testing"

	"github.com/hashicorp/boundary/internal/db"
	"github.com/hashicorp/boundary/internal/iam"
	"github.com/hashicorp/boundary/internal/kms"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/require"
)

func TestAuthToken(t *testing.T, conn *gorm.DB, kms *kms.Kms, scopeId, accountId string, opt ...Option) *AuthToken {
	t.Helper()
	ctx := context.Background()
	rw := db.New(conn)
	iamRepo, err := iam.NewRepository(rw, rw, kms)
	require.NoError(t, err)

	u := iam.TestUser(t, iamRepo, scopeId, iam.WithAccountIds(accountId))

	repo, err := NewRepository(rw, rw, kms)
	require.NoError(t, err)

	at, err := repo.CreateAuthToken(ctx, u, accountId, opt...)
	require.NoError(t, err)
	return at
}
