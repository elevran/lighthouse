package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	lhconst "github.com/submariner-io/lighthouse/pkg/constants"
	"github.com/submariner-io/lighthouse/pkg/lhutil"
	"github.com/submariner-io/lighthouse/pkg/mcs"
	"github.com/submariner-io/lighthouse/pkg/mcshub/controller"
)

const (
	serviceName = "svc"
	serviceNS   = "svc-ns"
	brokerNS    = "submariner-broker"
	cluster1    = "c1"
	cluster2    = "c2"
)

var (
	timeout      int32 = 10
	www          int32 = 80
	wwwAlternate int32 = 8080
	service            = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: serviceNS,
		},
		Spec: corev1.ServiceSpec{
			Type:            corev1.ServiceTypeClusterIP,
			SessionAffinity: corev1.ServiceAffinityClientIP,
			SessionAffinityConfig: &corev1.SessionAffinityConfig{
				ClientIP: &corev1.ClientIPConfig{
					TimeoutSeconds: &timeout,
				},
			},
			Ports: []corev1.ServicePort{
				{Port: www, Name: "http", Protocol: corev1.ProtocolTCP},
			},
		},
	}
	export = &mcsv1a1.ServiceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: serviceNS,
			Labels: map[string]string{
				lhconst.LabelSourceNamespace:      serviceNS,
				lhconst.LighthouseLabelSourceName: serviceName,
			},
		},
	}
)

// tests that a single export is reconciled into an import and marked as non-conflicting
func TestImportGenerated(t *testing.T) {
	assert := require.New(t)
	exp, err := prepareServiceExport(export, cluster1)
	assert.Nil(err)

	preloadedObjects := []runtime.Object{exp}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp.GetName(),
			Namespace: exp.GetNamespace(),
		}}

	result, err := ser.Reconcile(context.TODO(), req)
	assert.Nil(err)

	assert.False(result.Requeue, "unexpected requeue")

	si := &mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, si)
	assert.Nil(err)
	validateImportForExportedService(assert, si, exp)

	err = ser.Client.Get(context.TODO(), req.NamespacedName, exp)
	assert.Nil(err)
	assert.Len(exp.GetFinalizers(), 1, "expected finalizer, found ", exp.GetFinalizers())

	conflict := lhutil.GetServiceExportCondition(&exp.Status, mcsv1a1.ServiceExportConflict)
	if conflict == nil || conflict.Status != corev1.ConditionFalse {
		t.Errorf("expected conflict to be unset, found %v", conflict)
	}
}

// tests that reconciling multiple exports results in multiple imports, without conflicts
func TestMultipleExports(t *testing.T) {
	assert := require.New(t)
	exp1, err := prepareServiceExport(export, cluster1)
	assert.Nil(err)
	exp2, err := prepareServiceExport(export, cluster2)
	assert.Nil(err)

	preloadedObjects := []runtime.Object{exp1, exp2}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}

	_, err = ser.Reconcile(context.TODO(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp1.GetName(),
			Namespace: exp1.GetNamespace(),
		}})
	assert.Nil(err)
	_, err = ser.Reconcile(context.TODO(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp2.GetName(),
			Namespace: exp2.GetNamespace(),
		}})
	assert.Nil(err)

	imports := &mcsv1a1.ServiceImportList{}
	err = ser.Client.List(context.TODO(), imports, client.InNamespace(brokerNS))
	assert.Nil(err)

	exports := map[string]*mcsv1a1.ServiceExport{
		exp1.GetName(): exp1,
		exp2.GetName(): exp2,
	}
	for _, si := range imports.Items {
		validateImportForExportedService(assert, &si, exports[si.GetName()])
	}

	assert.Len(imports.Items, 2, "expected 2 imports got", len(imports.Items))

	exportList := &mcsv1a1.ServiceExportList{}
	err = ser.Client.List(context.TODO(), exportList, client.InNamespace(brokerNS))
	assert.Nil(err)

	for _, exp := range exportList.Items {
		hasConflict(assert, &exp, false)
	}
}

// since we're using a fake client and not a real k8s API endpoint, the import is not
// garbage collected. We do validate the finalizer is cleared...
func TestCreateAndDeleteExport(t *testing.T) {
	assert := require.New(t)
	exp, err := prepareServiceExport(export, cluster1)
	assert.Nil(err)

	preloadedObjects := []runtime.Object{exp}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp.GetName(),
			Namespace: exp.GetNamespace(),
		}}

	_, err = ser.Reconcile(context.TODO(), req)
	assert.Nil(err)

	err = ser.Client.Get(context.TODO(), req.NamespacedName, exp)
	assert.Nil(err)

	now := metav1.Now()
	exp.SetDeletionTimestamp(&now) // emulate a delete by setting the timestamp
	err = ser.Client.Update(context.TODO(), exp, &client.UpdateOptions{})
	assert.Nil(err)

	_, err = ser.Reconcile(context.TODO(), req)
	assert.Nil(err)

	exp = &mcsv1a1.ServiceExport{} // start with a fresh copy so we don't see stale data
	err = ser.Client.Get(context.TODO(), req.NamespacedName, exp)
	assert.Nil(err)

	assert.Len(exp.GetFinalizers(), 0, "expected no finalizers, found", exp.GetFinalizers())
}

// tests that change in non essential export properties don't change the import
func TestUpdateExportWithoutImport(t *testing.T) {
	assert := require.New(t)
	exp, err := prepareServiceExport(export, cluster1)
	assert.Nil(err)

	preloadedObjects := []runtime.Object{exp}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp.GetName(),
			Namespace: exp.GetNamespace(),
		}}

	_, err = ser.Reconcile(context.TODO(), req)
	assert.Nil(err)

	// save the generated import
	pre := &mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, pre)
	assert.Nil(err)

	// modify the export's label and reconcile again
	exp = &mcsv1a1.ServiceExport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, exp)
	assert.Nil(err)
	exp.GetLabels()["app"] = "foo"
	err = ser.Client.Update(context.TODO(), exp, &client.UpdateOptions{})
	assert.Nil(err)

	_, err = ser.Reconcile(context.TODO(), req)
	assert.Nil(err)

	post := &mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, post)
	assert.Nil(err)

	assert.Equal(pre.Spec, post.Spec)
	// @todo: can't compare entire object as resource version changes (reconcile always calls update)
}

// tests that change in essential export properties changes the import
func TestUpdateExportAndImport(t *testing.T) {
	assert := require.New(t)
	exp, err := prepareServiceExport(export, cluster1)
	assert.Nil(err)

	preloadedObjects := []runtime.Object{exp}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp.GetName(),
			Namespace: exp.GetNamespace(),
		}}

	_, err = ser.Reconcile(context.TODO(), req)
	assert.Nil(err)

	// save the generated import
	pre := &mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, pre)
	assert.Nil(err)

	// modify the export's port and reconcile again
	exp = &mcsv1a1.ServiceExport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, exp)
	assert.Nil(err)
	spec := &mcs.ExportSpec{}
	err = spec.UnmarshalObjectMeta(&exp.ObjectMeta)
	assert.Nil(err)
	spec.Service.Ports = append(spec.Service.Ports, mcsv1a1.ServicePort{Port: 443})
	err = spec.MarshalObjectMeta(&exp.ObjectMeta)
	assert.Nil(err)
	err = ser.Client.Update(context.TODO(), exp, &client.UpdateOptions{})
	assert.Nil(err)

	_, err = ser.Reconcile(context.TODO(), req)
	assert.Nil(err)

	post := &mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, post)
	assert.Nil(err)

	assert.NotEqual(pre.Spec, post.Spec)
	assert.Len(post.Spec.Ports, len(spec.Service.Ports))
}

// tests handling of conflicting exports
func TestExportConflict(t *testing.T) {
	assert := require.New(t)
	exp1, err := prepareServiceExport(export, cluster1)
	assert.Nil(err)
	// change the port number on the service
	exp2, err := prepareServiceExport(export, cluster2)
	assert.Nil(err)
	exp2.CreationTimestamp.Add(1 * time.Second)
	spec := &mcs.ExportSpec{}
	err = spec.UnmarshalObjectMeta(&exp2.ObjectMeta)
	assert.Nil(err)
	spec.Service.Ports[0].Port = wwwAlternate
	err = spec.MarshalObjectMeta(&exp2.ObjectMeta)
	assert.Nil(err)

	preloadedObjects := []runtime.Object{exp1, exp2}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}

	_, err = ser.Reconcile(context.TODO(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp1.GetName(),
			Namespace: exp1.GetNamespace(),
		}})
	assert.Nil(err)
	_, err = ser.Reconcile(context.TODO(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp2.GetName(),
			Namespace: exp2.GetNamespace(),
		}})
	assert.Nil(err)

	imports := &mcsv1a1.ServiceImportList{}
	err = ser.Client.List(context.TODO(), imports, client.InNamespace(brokerNS))
	assert.Nil(err)

	assert.Len(imports.Items, 1, "expected 1 import got", len(imports.Items))

	exports := &mcsv1a1.ServiceExportList{}
	err = ser.Client.List(context.TODO(), exports, client.InNamespace(brokerNS))
	assert.Nil(err)

	for _, exp := range exports.Items {
		hasConflict(assert, &exp, exp.GetName() != exp1.GetName()) // export1 should NOT conflict
	}
}

// tests handling of conflict resolution
func TestExportConflictResolution(t *testing.T) {
	assert := require.New(t)
	exp1, err := prepareServiceExport(export, cluster1)
	assert.Nil(err)
	// change the port number on the service
	exp2, err := prepareServiceExport(export, cluster2)
	assert.Nil(err)
	exp2.CreationTimestamp.Add(1 * time.Second)
	spec := &mcs.ExportSpec{}
	err = spec.UnmarshalObjectMeta(&exp2.ObjectMeta)
	assert.Nil(err)
	spec.Service.Ports[0].Port = wwwAlternate
	err = spec.MarshalObjectMeta(&exp2.ObjectMeta)
	assert.Nil(err)

	preloadedObjects := []runtime.Object{exp1, exp2}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}

	_, err = ser.Reconcile(context.TODO(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp1.GetName(),
			Namespace: exp1.GetNamespace(),
		}})
	assert.Nil(err)
	_, err = ser.Reconcile(context.TODO(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp2.GetName(),
			Namespace: exp2.GetNamespace(),
		}})
	assert.Nil(err)

	imports := &mcsv1a1.ServiceImportList{}
	err = ser.Client.List(context.TODO(), imports, client.InNamespace(brokerNS))
	assert.Nil(err)

	assert.Len(imports.Items, 1, "expected 1 import got", len(imports.Items))

	// resolve the conflict by updating the port back
	exports := &mcsv1a1.ServiceExportList{}
	err = ser.Client.List(context.TODO(), exports, client.InNamespace(brokerNS))
	assert.Nil(err)

	for _, exp := range exports.Items {
		spec := &mcs.ExportSpec{}
		err = spec.UnmarshalObjectMeta(&exp.ObjectMeta)
		assert.Nil(err)
		if spec.Service.Ports[0].Port != www {
			spec.Service.Ports[0].Port = www
			err = spec.MarshalObjectMeta(&exp.ObjectMeta)
			assert.Nil(err)

			err = ser.Client.Update(context.TODO(), &exp, &client.UpdateOptions{})
			assert.Nil(err)
			_, err = ser.Reconcile(context.TODO(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      exp.GetName(),
					Namespace: exp.GetNamespace(),
				}})
			assert.Nil(err)
		}
	}

	exports = &mcsv1a1.ServiceExportList{}
	err = ser.Client.List(context.TODO(), exports, client.InNamespace(brokerNS))
	assert.Nil(err)
	assert.Len(exports.Items, 2, "expected 2 import got", len(exports.Items))

	for _, exp := range exports.Items {
		hasConflict(assert, &exp, false)
	}
}

// generate a ServiceExport matching service (global variable) and cluster
func prepareServiceExport(export *mcsv1a1.ServiceExport, cluster string) (*mcsv1a1.ServiceExport, error) {
	exp := export.DeepCopy()

	exp.ObjectMeta.CreationTimestamp = metav1.Now()
	exp.Labels[lhconst.LighthouseLabelSourceCluster] = cluster
	es, err := mcs.NewExportSpec(service, exp, cluster)
	if err != nil {
		return nil, err
	}
	err = es.MarshalObjectMeta(&exp.ObjectMeta)
	if err != nil {
		return nil, err
	}
	exp.Name = lhutil.GenerateObjectName(serviceName, serviceNS, cluster)
	exp.Namespace = brokerNS
	return exp, nil
}

// validate the Import matches expectations based on the export
func validateImportForExportedService(assert *require.Assertions, si *mcsv1a1.ServiceImport, exp *mcsv1a1.ServiceExport) {
	spec := &mcs.ExportSpec{}
	_ = spec.UnmarshalObjectMeta(&exp.ObjectMeta)

	assert.Equal(si.Name, exp.Name)
	assert.Equal(si.Namespace, exp.Namespace)
	assert.Equal(si.Spec.Type, spec.Service.Type)
	assert.Equal(len(si.Spec.Ports), len(spec.Service.Ports))
	for i, p := range si.Spec.Ports { // assumes the order is maintained if more than one port is used
		assert.Equal(p, spec.Service.Ports[i])
	}
	assert.Equal(si.Status.Clusters[0].Cluster, exp.Labels[lhconst.LighthouseLabelSourceCluster])
}

// validate the Conflict status is set to the desired status
func hasConflict(assert *require.Assertions, exp *mcsv1a1.ServiceExport, want bool) bool {
	conflict := lhutil.GetServiceExportCondition(&exp.Status, mcsv1a1.ServiceExportConflict)
	assert.NotNil(conflict)
	assert.Equal(want, conflict.Status == corev1.ConditionTrue, "conflict set incorrectly", exp.GetName(), conflict)
	return want == (conflict.Status == corev1.ConditionTrue)
}

// return a scheme with the default k8s and mcs objects
func getScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(mcsv1a1.AddToScheme(scheme))
	return scheme
}

// generate a fake client with preloaded objects
func getClient(objs []runtime.Object) client.Client {
	return fake.NewClientBuilder().WithScheme(getScheme()).WithRuntimeObjects(objs...).Build()
}

// satisfy the logr.Logger interface with a nil logger
type logger struct {
	enabled bool
	t       *testing.T
	name    string
	kv      map[string]interface{}
}

var _ logr.Logger = &logger{}

func newLogger(t *testing.T, enabled bool) *logger {
	return &logger{
		enabled: enabled,
		t:       t,
		kv:      make(map[string]interface{}),
	}
}

func (l logger) Enabled() bool {
	return l.enabled
}

func (l logger) Error(e error, msg string, keysAndValues ...interface{}) {
	if l.enabled {
		args := []interface{}{e.Error(), msg, keysAndValues, l.kv}
		l.t.Error(args...)
	}
}

func (l logger) Info(msg string, keysAndValues ...interface{}) {
	if l.enabled {
		args := []interface{}{msg, keysAndValues, l.kv}
		l.t.Log(args...)
	}
}

func (l *logger) V(level int) logr.Logger {
	return l
}

func (l *logger) WithValues(keysAndValues ...interface{}) logr.Logger {
	if len(keysAndValues)%2 != 0 {
		panic(keysAndValues)
	}

	for i := 0; i < len(keysAndValues); i += 2 {
		l.kv[keysAndValues[i].(string)] = keysAndValues[i+1]
	}
	return l
}

func (l *logger) WithName(name string) logr.Logger {
	l.name = name
	return l
}
