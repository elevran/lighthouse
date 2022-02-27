package mcs_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/submariner-io/lighthouse/pkg/mcs"
)

func TestMarshalUnmarshal(t *testing.T) {
	testcases := map[string]struct {
		spec *mcs.ExportSpec
	}{
		/*
			"empty":                  {},
			"missing: timestamp":     {},
			"missing: cluster":       {},
			"missing: namespace":     {},
			"missing: name":          {},
			"missing: service":       {},
			"missing: service.type":  {},
			"missing: service.ports": {},
			"simple spec":            {},
			"multiple ports":         {},
		*/
	}

	assertions := require.New(t)

	for name, test := range testcases {
		t.Logf("Running test case %s", name)

		md := &metav1.ObjectMeta{}
		err := test.spec.MarshalObjectMeta(md)
		assertions.NoError(err)

		spec := &mcs.ExportSpec{}
		err = spec.UnmarshalObjectMeta(md)
		assertions.NoError(err)

		assertions.Equal(test.spec, spec)
	}
}

func TestCompatibility(t *testing.T) {
	testcases := map[string]struct {
		spec  *mcs.ExportSpec
		other *mcs.ExportSpec
		field string
	}{
		"compatible: empty": {spec: &mcs.ExportSpec{}, other: &mcs.ExportSpec{}},
		/*
			"compatible: identical":        {},
			"compatible: local properties": {},
			"conflicting: type":            {},
			"conflicting: affinity":        {},
			"conflicting: port":            {},
		*/
	}

	assertions := require.New(t)

	for name, test := range testcases {
		t.Logf("Running test case %s", name)

		compatible, field := test.spec.IsCompatibleWith(test.other)
		assertions.True(compatible && field == "", "no field expected in conflict")
		assertions.Equal(field, test.field, "unexpected field conflict detected")
	}
}

func TestConflictResolutionCriteria(t *testing.T) {
	testcases := map[string]struct {
		spec      *mcs.ExportSpec
		other     *mcs.ExportSpec
		preferred bool
	}{
		"different time": {
			spec: &mcs.ExportSpec{
				CreatedAt: metav1.Now(),
			},
			other: &mcs.ExportSpec{
				CreatedAt: metav1.Now(),
			},
			preferred: true,
		},
		"equal time": {
			spec: &mcs.ExportSpec{
				CreatedAt: metav1.Unix(0, 0),
				ClusterID: "cluster1",
			},
			other: &mcs.ExportSpec{
				CreatedAt: metav1.Unix(0, 0),
				ClusterID: "cluster2",
			},
			preferred: true,
		},
	}

	assertions := require.New(t)

	for name, test := range testcases {
		t.Logf("Running test case %s", name)

		preferred := test.spec.IsPrefferredOver(test.other)
		assertions.Equal(test.preferred, preferred)
		reversed := test.other.IsPrefferredOver(test.spec)
		assertions.Equal(preferred, !reversed)
	}
}

func TestCreateFromObjects(t *testing.T) {
	t.Log("not implemented")
}
