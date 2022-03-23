package controller_test

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	serviceName  = "svc"
	serviceName2 = "svc2"
	serviceNS    = "svc-ns"
	brokerNS     = "submariner-broker"
	cluster1     = "c1"
	cluster2     = "c2"
)

var (
	timeout int32 = 10
	service       = &corev1.Service{
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
				{Port: 80, Name: "http0", Protocol: corev1.ProtocolTCP},
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

	differentPort = mcsv1a1.ServicePort{Port: 81, Name: "http1", Protocol: corev1.ProtocolTCP}
)

func prepareServiceExport(t *testing.T) (*mcsv1a1.ServiceExport, *mcs.ExportSpec) {
	exp := export.DeepCopy()
	exp.Labels[lhconst.LighthouseLabelSourceCluster] = cluster1
	es, err := mcs.NewExportSpec(service, exp, cluster1)
	if err != nil {
		t.Error(err)
	}
	err = es.MarshalObjectMeta(&exp.ObjectMeta)
	if err != nil {
		t.Error(err)
	}
	return exp, es
}

// Pass (to be deleted)
func TestImportGenerated(t *testing.T) {
	exp, es := prepareServiceExport(t)

	preloadedObjects := []runtime.Object{service, exp}
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

	// mimics the changing in the names that happens in the hub
	// TO PR - make sure it suppose to be after taking the names to the req. maybe don't need it?
	exp.Name = lhutil.GenerateObjectName(serviceName, serviceNS, cluster1)
	exp.Namespace = brokerNS

	result, err := ser.Reconcile(context.TODO(), req)

	if err != nil {
		t.Error(err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}

	si := mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, &si)

	if err != nil {
		t.Error(err)
	}
	assertions := require.New(t)
	// TO PR - I wanted to test "as close" to reality, and get it from the es and not initialized those values
	// maybe its not needed, and can be initialized
	excepted := generateExceptedServiceImport(exp, es)
	compareSi(&excepted, &si, assertions)
}

// Pass
func TestImportFromExport(t *testing.T) {
	exp, es := prepareServiceExport(t)

	preloadedObjects := []runtime.Object{service}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}
	//create *only* the export
	ser.Client.Create(context.TODO(), exp)
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp.GetName(),
			Namespace: exp.GetNamespace(),
		}}

	// mimics the changing in the names that happens in the hub
	//TO PR - make sure it suppose to be after taking the names to the req. maybe don't need it?
	exp.Name = lhutil.GenerateObjectName(serviceName, serviceNS, cluster1)
	exp.Namespace = brokerNS

	result, err := ser.Reconcile(context.TODO(), req)

	if err != nil {
		t.Error(err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}

	si := mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, &si)

	if err != nil {
		t.Error(err)
	}
	// TO PR - I wanted to test "as close" to reality, and get it from the es and not initialized those values
	// maybe its not needed, and can be initialized
	excepted := generateExceptedServiceImport(exp, es)
	assertions := require.New(t)
	// make sure that the right serviceImport was created
	compareSi(&excepted, &si, assertions)
}

/*

OLD VERSION - trying to use the client to create seperatlly

func Test2ImportsFromExports(t *testing.T) {
	exp1, es1 := prepareServiceExport(t)

	preloadedObjects := []runtime.Object{service}
	ser := mcscontroller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}
	ser.Client.Create(context.TODO(), exp1)
	req1 := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp1.GetName(),
			Namespace: exp1.GetNamespace(),
		}}

	// mimics the changing in the names that happens in the hub
	// TO PR - make sure it suppose to be after taking the names to the req. maybe don't need it?
	exp1.Name = lhutil.GenerateObjectName(serviceName, serviceNS, cluster1)
	exp1.Namespace = brokerNS

	result, err := ser.Reconcile(context.TODO(), req1)

	if err != nil {
		t.Error(err)
	}

	if result.Requeue {
		t.Error("unexpected requeue")
	}

	si := mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req1.NamespacedName, &si)

	if err != nil {
		t.Error(err)
	}
	assertions := require.New(t)
	excepted := generateExceptedServiceImport(exp1, es1)
	compareSi(&excepted, &si, assertions)

	exp2, es2 := prepareServiceExport(t)
	exp2.Labels[lhconst.LighthouseLabelSourceCluster] = cluster2

	ser.Client.Create(context.TODO(), exp2)
	req2 := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp2.GetName(),
			Namespace: exp2.GetNamespace(),
		}}

	// mimics the changing in the names that happens in the hub
	//TO PR - make sure it suppose to be after taking the names to the req. maybe don't need it?
	exp2.Name = lhutil.GenerateObjectName(serviceName, serviceNS, cluster2)
	exp2.Namespace = brokerNS
	exp2.Labels[lhconst.LighthouseLabelSourceCluster] = cluster2 // changed field
	result, err = ser.Reconcile(context.TODO(), req2)

	if err != nil {
		t.Error(err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}

	err = ser.Client.Get(context.TODO(), req2.NamespacedName, &si)

	if err != nil {
		t.Error(err)
	}

	excepted2 := generateExceptedServiceImport(exp2, es2)
	compareSi(&excepted2, &si, assertions)
} */

// Pass - I still needs to make sure it really testing this feature ok, didn't had the time to make sure its ok
func Test2ImportsFromExports(t *testing.T) {
	exp1, es1 := prepareServiceExport(t)
	exp2, es2 := prepareServiceExport(t)
	exp2.Name = serviceName2                                      // won't let me add 2 serviceExports with same name - probably not the field I should have edit.
	exp2.Labels[lhconst.LighthouseLabelSourceName] = serviceName2 // changed field
	exp2.Labels[lhconst.LighthouseLabelSourceCluster] = cluster2

	preloadedObjects := []runtime.Object{service, exp1, exp2}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}

	req1 := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp1.GetName(),
			Namespace: exp1.GetNamespace(),
		}}

	result, err := ser.Reconcile(context.TODO(), req1)

	if err != nil {
		t.Error(err)
	}

	if result.Requeue {
		t.Error("unexpected requeue")
	}

	// check si1:
	si := mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req1.NamespacedName, &si)

	if err != nil {
		t.Error(err)
	}
	assertions := require.New(t)
	excepted := generateExceptedServiceImport(exp1, es1)
	compareSi(&excepted, &si, assertions)

	// check si2:
	req2 := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp2.GetName(),
			Namespace: exp2.GetNamespace(),
		}}

	result, err = ser.Reconcile(context.TODO(), req2)

	if err != nil {
		t.Error(err)
	}

	if result.Requeue {
		t.Error("unexpected requeue")
	}

	err = ser.Client.Get(context.TODO(), req2.NamespacedName, &si)

	if err != nil {
		t.Error(err)
	}

	excepted2 := generateExceptedServiceImport(exp2, es2)
	compareSi(&excepted2, &si, assertions)
}

// FAILS - The service import is not deleted after deleting the service export
// not sure why - The Reconcil function is finishing eraly since it sees that the se object is deleted
// I think it should delete the service import too since its owner is the serviceExport that was deleted.
func TestCreateAndDeleteExport(t *testing.T) {
	assertions := require.New(t)
	exp, es := prepareServiceExport(t)
	preloadedObjects := []runtime.Object{service}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}
	ser.Client.Create(context.TODO(), exp)
	req1 := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp.GetName(),
			Namespace: exp.GetNamespace(),
		}}
	result, err := ser.Reconcile(context.TODO(), req1)

	if err != nil {
		t.Error(err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}

	si := mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req1.NamespacedName, &si)

	if err != nil {
		t.Error(err)
	}
	// TO PR - I wanted to test "as close" to reality, and get it from the es and not initialized those values
	// maybe its not needed, and can be initialized
	excepted := generateExceptedServiceImport(exp, es)
	compareSi(&excepted, &si, assertions)

	//delete the export - and check that the import was also deleted:
	ser.Client.Delete(context.TODO(), exp)
	result, err = ser.Reconcile(context.TODO(), req1)

	if err != nil {
		t.Error(err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}
	si = mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req1.NamespacedName, &si)
	assertions.True(apierrors.IsNotFound(err))
}

// not finished - need to change some field (!)
func TestUpdateExportWithoutImport(t *testing.T) {
	exp1, _ := prepareServiceExport(t)
	preloadedObjects := []runtime.Object{service, exp1}
	ser := controller.ServiceExportReconciler{
		Client: getClient(preloadedObjects),
		Log:    newLogger(t, false),
		Scheme: getScheme(),
	}
	req1 := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      exp1.GetName(),
			Namespace: exp1.GetNamespace(),
		}}
	result, err := ser.Reconcile(context.TODO(), req1)

	if err != nil {
		t.Error(err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}

	t.Skip()
	//update export: CHECK WHICK SLOT I CAN UPDATE AND DOSENT EFFECT THE IMPORT
}

// Fails - maybe the client dosnt really update? not sure the conroller is really wathcing, but the reconcile should cover it..
func TestUpdateExportAndImport(t *testing.T) {
	exp, es := prepareServiceExport(t)
	preloadedObjects := []runtime.Object{service, exp}
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
	if err != nil {
		t.Error(err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}

	si := mcsv1a1.ServiceImport{}
	err = ser.Client.Get(context.TODO(), req.NamespacedName, &si)
	if err != nil {
		t.Error(err)
	}
	// make sure that the right serviceImport was created
	excepted := generateExceptedServiceImport(exp, es)
	assertions := require.New(t)
	compareSi(&excepted, &si, assertions)

	// update the export:
	es.Service.Ports = append(es.Service.Ports, differentPort)
	err = es.MarshalObjectMeta(&exp.ObjectMeta)
	if err != nil {
		t.Error(err)
	}

	err = ser.Client.Update(context.TODO(), exp)
	if err != nil {
		//fails here - "Operation cannot be fulfilled on serviceexports.multicluster.x-k8s.io "svc": object was modified"
		t.Error(err)
	}

	result, err = ser.Reconcile(context.TODO(), req)
	if err != nil {
		t.Error(err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}

	excepted = generateExceptedServiceImport(exp, es)
	compareSi(&excepted, &si, assertions)
}

func TestExportConflict(t *testing.T) {
	t.Skip()
	panic("Not implemented")
}

// To PR - is those fields check ok? are the name and namespace should be in this format?
// compare two service imports field and raise error if they are not with identical values
func compareSi(excepted *mcsv1a1.ServiceImport, si *mcsv1a1.ServiceImport, assertions *require.Assertions) {
	assertions.Equal(excepted.ObjectMeta.Name, si.ObjectMeta.Name)
	assertions.Equal(excepted.ObjectMeta.Namespace, si.ObjectMeta.Namespace)
	assertions.Equal(excepted.ObjectMeta.CreationTimestamp, si.ObjectMeta.CreationTimestamp) // not sure if it's even a relevant field
	assertions.Equal(excepted.Spec.Type, si.Spec.Type)
	comparePorts(excepted.Spec.Ports, si.Spec.Ports, assertions)
	compareClusters(excepted.Status.Clusters, si.Status.Clusters, assertions)
}

// compare two ports arrays and raise error if they are not with identical values
func comparePorts(excepted []mcsv1a1.ServicePort, ports []mcsv1a1.ServicePort, assertions *require.Assertions) {
	assertions.Equal(len(excepted), len(ports))
	for i, port := range excepted {
		assertions.Equal(port, ports[i])
	}
}

// compare two ports arrays and raise error if they are not with identical values
func compareClusters(excepted []mcsv1a1.ClusterStatus, cs []mcsv1a1.ClusterStatus, assertions *require.Assertions) {
	assertions.Equal(len(excepted), len(cs))
	for i, cluster := range cs {
		assertions.Equal(cluster, excepted[i])
	}
}

func generateExceptedServiceImport(exp *mcsv1a1.ServiceExport, es *mcs.ExportSpec) mcsv1a1.ServiceImport {
	si := mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: lhutil.GetOriginalObjectName(exp.ObjectMeta).Namespace,
			Name:      lhutil.GetOriginalObjectName(exp.ObjectMeta).Name,
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  es.Service.Type,
			Ports: es.Service.Ports,
		},
		Status: mcsv1a1.ServiceImportStatus{
			Clusters: []mcsv1a1.ClusterStatus{{Cluster: exp.ObjectMeta.Labels[lhconst.LighthouseLabelSourceCluster]}},
		},
	}
	return si
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
