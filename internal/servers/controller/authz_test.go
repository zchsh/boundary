package controller_test

import (
	"testing"

	"github.com/hashicorp/boundary/api"
	"github.com/hashicorp/boundary/api/authmethods"
	"github.com/hashicorp/boundary/api/roles"
	"github.com/hashicorp/boundary/api/scopes"
	"github.com/hashicorp/boundary/internal/servers/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getUserClientWithGrant(g string) *api.Client {
	return nil
}

func TestGrantChecks_Default(t *testing.T) {
	tc := controller.NewTestController(t, nil)
	defer tc.Shutdown()

	client := tc.Client()
	// By default the anonymous user can list scopes and authenticate.
	sClient := scopes.NewClient(client)
	l, apiErr, err := sClient.List(tc.Context(), "global")
	require.NoError(t, err)
	require.Nil(t, apiErr)
	assert.Empty(t, l)

	amClient := authmethods.NewClient(client)
	at, apiErr, err := amClient.Authenticate(tc.Context(), controller.DefaultTestAuthMethodId, map[string]interface{}{
		"login_name": controller.DefaultTestLoginName,
		"password":   controller.DefaultTestPassword,
	})
	require.NoError(t, err)
	require.Nil(t, apiErr)

	client.SetToken(at.Token)
	rClient := roles.NewClient(client)

	// Setup a fresh role state for testing
	r, apiErr, err := rClient.Create(tc.Context(), "global", roles.WithDescription("Made for testing."))
	require.NoError(t, err)
	require.Nil(t, apiErr)

	r, apiErr, err = rClient.AddPrincipals(tc.Context(), r.Id, 0, []string{at.UserId}, roles.WithAutomaticVersioning(true))
	require.NoError(t, err)
	require.Nil(t, apiErr)

	r, apiErr, err = rClient.AddGrants(tc.Context(), r.Id, r.Version, []string{"type=role;actions=add-grants", "type=role;actions=list"})
	require.NoError(t, err)
	require.Nil(t, apiErr)

	// Clear out all the previous roles.
	rl, apiErr, err := rClient.List(tc.Context(), "global")
	require.NoError(t, err)
	require.Nil(t, apiErr)

	for _, ir := range rl {
		if ir.Id != r.Id {
			_, apiErr, err = rClient.Delete(tc.Context(), ir.Id)
			require.NoError(t, err)
			require.Nil(t, apiErr)
		}
	}

	rl, apiErr, err = rClient.List(tc.Context(), "global")
	require.NoError(t, err)
	require.Nil(t, apiErr)
	assert.Len(t, rl, 1)

	r, apiErr, err = rClient.AddGrants(tc.Context(), r.Id, r.Version, []string{"type=role;actions=read"})
	require.NoError(t, err)
	require.Nil(t, apiErr)

	for _, ir := range rl {
		ir, apiErr, err = rClient.Read(tc.Context(), ir.Id)
		require.NoError(t, err)
		require.Nil(t, apiErr)
		t.Logf("Got role %+v", ir)
	}
}
