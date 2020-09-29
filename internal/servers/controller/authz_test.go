package controller_test

import (
	"errors"
	"testing"

	"github.com/hashicorp/boundary/api"
	"github.com/hashicorp/boundary/api/accounts"
	"github.com/hashicorp/boundary/api/authmethods"
	"github.com/hashicorp/boundary/api/groups"
	"github.com/hashicorp/boundary/api/hostcatalogs"
	"github.com/hashicorp/boundary/api/hosts"
	"github.com/hashicorp/boundary/api/hostsets"
	"github.com/hashicorp/boundary/api/roles"
	"github.com/hashicorp/boundary/api/scopes"
	"github.com/hashicorp/boundary/api/targets"
	"github.com/hashicorp/boundary/api/users"
	"github.com/hashicorp/boundary/internal/servers/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getUserClientWithGrants(t *testing.T, tc *controller.TestController, g []string) *api.Client {
	client := tc.Client()

	adminAuthMethodClient := authmethods.NewClient(client)
	resp, err := adminAuthMethodClient.Authenticate(tc.Context(), controller.DefaultTestAuthMethodId, map[string]interface{}{
		"login_name": controller.DefaultTestLoginName,
		"password":   controller.DefaultTestPassword,
	})
	require.NoError(t, err)

	at := resp.Item
	client.SetToken(at.Token)
	adminRoleClient := roles.NewClient(client)
	adminAuthMethodClient = authmethods.NewClient(client)

	adminRole := "r_default"
	adminAccount := at.AccountId
	adminUser := at.UserId

	_, err = adminRoleClient.SetPrincipals(tc.Context(), adminRole, 0, []string{at.UserId}, roles.WithAutomaticVersioning(true))
	require.NoError(t, err)

	_, err = adminRoleClient.SetGrants(tc.Context(), adminRole, 0, []string{
		"type=role;actions=set-grants",
		"type=role;actions=set-principals",
		"type=role;actions=read",
		"type=role;actions=list",
		"type=role;actions=create",
		"type=role;actions=delete",
		"type=auth-method;actions=authenticate",
		"type=account;actions=create",
		"type=account;actions=list",
		"type=account;actions=delete",
		"type=user;actions=list",
		"type=user;actions=delete"},
		roles.WithAutomaticVersioning(true))
	require.NoError(t, err)

	// Clear out all the previous roles.
	rListResp, err := adminRoleClient.List(tc.Context(), "global")
	require.NoError(t, err)

	// Remove all roles that are not the requested.
	for _, ir := range rListResp.Items {
		if ir.Id != adminRole {
			_, err = adminRoleClient.Delete(tc.Context(), ir.Id)
			require.NoError(t, err)
		}
	}

	// Remove all accounts that are not admin
	adminAccountClient := accounts.NewClient(client)
	acctListResp, err := adminAccountClient.List(tc.Context(), at.AuthMethodId)
	require.NoError(t, err)
	for _, ia := range acctListResp.Items {
		if ia.Id != adminAccount {
			_, err = adminAccountClient.Delete(tc.Context(), ia.Id)
			require.NoError(t, err)
		}
	}

	// Remove all users which are not admin
	adminUserClient := users.NewClient(client)
	uListResp, err := adminUserClient.List(tc.Context(), "global")
	require.NoError(t, err)
	for _, iu := range uListResp.Items {
		if iu.Id != adminUser && iu.Id != "u_anon" && iu.Id != "u_auth" && iu.Id != "u_recovery" {
			_, err = adminUserClient.Delete(tc.Context(), iu.Id)
			require.NoError(t, err)
		}
	}


	// Now create a new account, and role with the provided permissions.
	clientLoginName := "testingloginname"
	clientPassword := "testingpassword"
	_, err = adminAccountClient.Create(tc.Context(), at.AuthMethodId, accounts.WithPasswordAccountLoginName(clientLoginName), accounts.WithPasswordAccountPassword(clientPassword))
	require.NoError(t, err)

	clientToken, err := adminAuthMethodClient.Authenticate(tc.Context(), at.AuthMethodId, map[string]interface{}{"login_name": clientLoginName, "password": clientPassword})
	require.NoError(t, err)

	clientRole, err := adminRoleClient.Create(tc.Context(), "global")
	require.NoError(t, err)

	_, err = adminRoleClient.SetPrincipals(tc.Context(), clientRole.Item.Id, 0, []string{clientToken.Item.UserId}, roles.WithAutomaticVersioning(true))
	require.NoError(t, err)
	_, err = adminRoleClient.SetGrants(tc.Context(), clientRole.Item.Id, 0, g, roles.WithAutomaticVersioning(true))
	require.NoError(t, err)

	euClient := client.Clone()
	euClient.SetToken(clientToken.Item.Token)

	return euClient
}

func TestGrantChecks_Default(t *testing.T) {
	tc := controller.NewTestController(t, nil)
	t.Cleanup(tc.Shutdown)

	type testFunc func(*testing.T, *api.Client)

	cases := []struct {
		name       string
		grants     []string
		operations testFunc
	}{
		{
			name: "create-read-roles",
			grants: []string{"type=role;actions=create,read"},
			operations: func(t *testing.T, c *api.Client) {
				rc := roles.NewClient(c)
				rr, err := rc.Create(tc.Context(), "global")
				assert.NoError(t, err)
				_, err = rc.Read(tc.Context(), rr.Item.Id)
				assert.NoError(t, err)
				// Cant do other actions on same type
				_, err = rc.List(tc.Context(), "global")
				assert.True(t, errors.Is(err, api.ErrPermissionDenied), "Got %s, wanted Permission denied error", err)
				_, err = rc.Update(tc.Context(), rr.Item.Id, 0, roles.WithName("test"), roles.WithAutomaticVersioning(true))
				assert.True(t, errors.Is(err, api.ErrPermissionDenied), "Got %s, wanted Permission denied error", err)
				_, err = rc.Delete(tc.Context(), rr.Item.Id)
				assert.True(t, errors.Is(err, api.ErrPermissionDenied), "Got %s, wanted Permission denied error", err)
				// cant do same actions on different type
				uc := users.NewClient(c)
				_, err = uc.Create(tc.Context(), "global")
				assert.True(t, errors.Is(err, api.ErrPermissionDenied), "Got %s, wanted Permission denied error", err)
			},
		},
		{
			name: "create-read-users",
			grants: []string{"type=user;actions=create,read"},
			operations: func(t *testing.T, c *api.Client) {
				uc := users.NewClient(c)
				ur, err := uc.Create(tc.Context(), "global")
				assert.NoError(t, err)
				_, err = uc.Read(tc.Context(), ur.Item.Id)
				assert.NoError(t, err)
				// Cant do other actions on same type
				_, err = uc.List(tc.Context(), "global")
				assert.True(t, errors.Is(err, api.ErrPermissionDenied), "Got %#v, wanted Permission denied error", err)
				_, err = uc.Update(tc.Context(), ur.Item.Id, 0, users.WithName("test"), users.WithAutomaticVersioning(true))
				assert.True(t, errors.Is(err, api.ErrPermissionDenied), "Got %#v, wanted Permission denied error", err)
				_, err = uc.Delete(tc.Context(), ur.Item.Id)
				assert.True(t, errors.Is(err, api.ErrPermissionDenied), "Got %#v, wanted Permission denied error", err)
				// cant do same actions on different type
				rc := roles.NewClient(c)
				_, err = rc.Create(tc.Context(), "global")
				assert.True(t, errors.Is(err, api.ErrPermissionDenied), "Got %#v, wanted Permission denied error", err)
			},
		},
		{
			name: "create-read-all",
			grants: []string{"id=*;actions=create,read"},
			operations: func(t *testing.T, c *api.Client) {
				sc := scopes.NewClient(c)
				sr, err := sc.Create(tc.Context(), "global", scopes.WithSkipRoleCreation(true))
				require.NoError(t, err)
				_, err = sc.Read(tc.Context(), sr.Item.Id)
				assert.NoError(t, err)

				amc := authmethods.NewClient(c)
				amr, err := amc.Create(tc.Context(), "password", sr.Item.Id)
				require.NoError(t, err)
				_, err = amc.Read(tc.Context(), amr.Item.Id)
				assert.NoError(t, err)

				acc := accounts.NewClient(c)
				acr, err := acc.Create(tc.Context(), amr.Item.Id, accounts.WithPasswordAccountLoginName("something"), accounts.WithPasswordAccountPassword("something"))
				require.NoError(t, err)
				_, err = acc.Read(tc.Context(), acr.Item.Id)
				assert.NoError(t, err)

				uc := users.NewClient(c)
				ur, err := uc.Create(tc.Context(), sr.Item.Id)
				require.NoError(t, err)
				_, err = uc.Read(tc.Context(), ur.Item.Id)
				assert.NoError(t, err)

				gc := groups.NewClient(c)
				gr, err := gc.Create(tc.Context(), sr.Item.Id)
				require.NoError(t, err)
				_, err = gc.Read(tc.Context(), gr.Item.Id)
				assert.NoError(t, err)

				hcc := hostcatalogs.NewClient(c)
				hcr, err := hcc.Create(tc.Context(), "static", sr.Item.Id)
				require.NoError(t, err)
				_, err = hcc.Read(tc.Context(), hcr.Item.Id)
				assert.NoError(t, err)

				hsc := hostsets.NewClient(c)
				hsr, err := hsc.Create(tc.Context(), hcr.Item.Id)
				require.NoError(t, err)
				_, err = hsc.Read(tc.Context(), hsr.Item.Id)
				assert.NoError(t, err)

				hc := hosts.NewClient(c)
				hr, err := hc.Create(tc.Context(), hcr.Item.Id)
				require.NoError(t, err)
				_, err = hc.Read(tc.Context(), hr.Item.Id)
				assert.NoError(t, err)

				rc := roles.NewClient(c)
				rr, err := rc.Create(tc.Context(), sr.Item.Id)
				require.NoError(t, err)
				_, err = rc.Read(tc.Context(), rr.Item.Id)
				assert.NoError(t, err)

				tarC := targets.NewClient(c)
				tr, err := tarC.Create(tc.Context(),"tcp", sr.Item.Id)
				require.NoError(t, err)
				_, err = tarC.Read(tc.Context(), tr.Item.Id)
				assert.NoError(t, err)
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			client := getUserClientWithGrants(t, tc, tt.grants)
			tt.operations(t, client)
		})
	}
}
