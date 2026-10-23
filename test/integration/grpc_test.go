package integration_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pthm/orchard/pkg/provider/grpc/grpctest"
)

// TestGRPC calls the test Accounts service, wires the returned id into
// a second call and a Postgres insert, over server reflection and over
// a descriptor set; and a bad enum value fails the pre-run check, so
// nothing executes.
func TestGRPC(t *testing.T) {
	descriptorSet, err := filepath.Abs("../../pkg/provider/grpc/grpctest/testdata/accounts.binpb")
	require.NoError(t, err)

	for _, c := range []struct {
		name string
		opts grpctest.Options
		set  string
	}{
		{"reflection", grpctest.Options{}, ""},
		{"descriptor set", grpctest.Options{NoReflection: true}, descriptorSet},
	} {
		t.Run(c.name, func(t *testing.T) {
			resetDB(t)
			addr, stop, err := grpctest.Start(c.opts)
			require.NoError(t, err)
			t.Cleanup(stop)
			vars := map[string]string{"dsn": testDSN, "address": addr, "descriptor_set": c.set}

			out := runScenario(t, "testdata/scenarios/grpc.hcl", vars)
			assert.Equal(t, "org-1", ctyString(t, out["org_id"]))
			assert.Equal(t, int64(41), ctyInt(t, out["owner_age"]))
			assert.Equal(t, "org-1", ctyString(t, out["merchant"]), "the id was wired into Postgres")
			assert.Equal(t, "org-1", ctyString(t, out["request_id"]))

			vars["plan"] = "PLAN_PRO"
			err = validateLive(t, "testdata/scenarios/grpc.hcl", vars)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `field "plan" is accounts.v1.Plan (PLAN_UNSPECIFIED, PLAN_FREE, PLAN_TEAM); got "PLAN_PRO"`)
		})
	}
}
