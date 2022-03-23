/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mcshub

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	"github.com/submariner-io/lighthouse/pkg/lhutil"
	"github.com/submariner-io/lighthouse/pkg/mcs"
)

const (
	serviceExportFinalizerName = "lighthouse.submariner.io/service-export-finalizer"
)

// ServiceExportReconciler reconciles a ServiceExport object
type ServiceExportReconciler struct {
	Client client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=serviceexports,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=serviceexports/finalizers,verbs=get;update;delete
// +kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=serviceexports/status,verbs=get;update
// +kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=serviceimports,verbs=create;get;list;update;patch;delete

// Reconcile handles an update to a ServiceExport
// By design, the event that triggered reconciliation is not passed to the reconciler
// to ensure actions are based on state alone. This approach is referred to as level-based,
// as opposed to edge-based.
func (r *ServiceExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	log := r.Log.WithValues("serviceexport", req.NamespacedName)

	// @todo extract and log the real (i.e., source) object name, namespace and cluster?
	log.Info("Reconciling ServiceExport")

	se := &mcsv1a1.ServiceExport{}
	if err := r.Client.Get(ctx, req.NamespacedName, se); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("No ServiceExport found")
		} else {
			// @tdo avoid requeuing for now. Revisit once we see what errors do show.
			log.Error(err, "Error fetching ServiceExport")
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(se, serviceExportFinalizerName) && se.GetDeletionTimestamp() == nil {
		controllerutil.AddFinalizer(se, serviceExportFinalizerName)
		if err := r.Client.Update(ctx, se); err != nil {
			if apierrors.IsConflict(err) { // The SE has been updated since we read it, requeue to retry reconciliation.
				return ctrl.Result{Requeue: true}, nil
			}
			log.Error(err, "Error adding finalizer")
			return ctrl.Result{}, err
		}
	}
	listOptions, err := lhutil.NewServiceExportListFilter(se.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	result, err := r.reconcile(ctx, lhutil.GetOriginalObjectName(se.ObjectMeta), listOptions)
	if err != nil {
		return result, err
	}

	if se.GetDeletionTimestamp() != nil { // service export is marked to be deleted
		return r.handleDelete(ctx, se)
	}

	return result, err
}

// reconcile the cluster state.
//
// Reconciliation logic:
// 1. list all relevant exports (i.e., having the same name and namespace labels)
// 2. determine the primary export object
// 3. iterate on all relevant export objects
// 3a. if the object is being deleted (non empty deletion timestamp) - ignore
// 3b. if object conflicts with primary - update export.Status.Condition.Conflict
//		and delete corresponding ServiceImport.
// 3c. otherwise, create/update the corrresponding ServiceImport and set the
//		ServiceExport as owner.
//
// Notes:
// - ensure objects are protected with a finalizer. Since we only receive the
//	 affected object name into the reconciliation loop, we have no way to determine
//	 and retrieve affected ServiceExports (i.e., client.List would not return the
//   deleted object and we can't access its labels). The finalizer is cleared when we
// 	 receive a notification for change in an object scheduled for deletion.
// - ServiceImports are owned by the corresponding ServiceExport and are thus
//	 automatically deleted by k8s when their ServiceExport is deleted.
//
func (r *ServiceExportReconciler) reconcile(ctx context.Context, name types.NamespacedName, listOptions *client.ListOptions) (ctrl.Result, error) {
	log := r.Log.WithValues("service", name)
	var exports mcsv1a1.ServiceExportList
	if err := r.Client.List(ctx, &exports, listOptions); err != nil {
		log.Error(err, "unable to list service's service export")
		return ctrl.Result{}, err
	}
	primary, err := getPrimaryExportObject(exports)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, se := range exports.Items {
		if se.DeletionTimestamp != nil {
			continue
		}

		exportSpec := &mcs.ExportSpec{}
		err := exportSpec.UnmarshalObjectMeta(&se.ObjectMeta)
		if err != nil {
			log.Info("Failed to unmarshal exportSpec from %s serviceExport in Namespace %s", se.Name, se.Namespace)
			return ctrl.Result{}, err
		}

		err = exportSpec.EnsureCompatible(primary)
		if err != nil {
			lhutil.UpdateServiceExportConditions(ctx, &se, log, mcsv1a1.ServiceExportConflict, corev1.ConditionTrue, "", err.Error())
			err = r.Client.Status().Update(ctx, &se)
			if err != nil {
				return ctrl.Result{}, err
			}
			si := mcsv1a1.ServiceImport{}
			err = r.Client.Get(ctx, name, &si)
			if err == nil {
				err = r.Client.Delete(ctx, &si)
				if err != nil {
					return ctrl.Result{}, err
				}
			} else {
				if !apierrors.IsNotFound(err) {
					log.Error(err, "Failed to get the service Import to delete")
					return ctrl.Result{}, err
				}
			}
			continue
		}
		lhutil.UpdateServiceExportConditions(ctx, &se, log, mcsv1a1.ServiceExportConflict, corev1.ConditionFalse, "", "successfully update the service export")
		err = r.Client.Status().Update(ctx, &se)
		if err != nil {
			return ctrl.Result{}, err
		}
		err = r.ensureImportFor(ctx, &se)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// getPrimaryExportObject finds the Primary ExportSpec from ServiceExportList.
// Retruns the ExportSpec object that determined as the primary one from
// the given ServiceExportList according to the conflict resolution specification set in the
// KEP, that is implemented in IsPreferredOver method of ExportSpec.
func getPrimaryExportObject(exportList mcsv1a1.ServiceExportList) (*mcs.ExportSpec, error) {
	if len(exportList.Items) == 0 {
		return nil, fmt.Errorf("exportList is empty - couldn't find primary export object")
	}
	var primary *mcs.ExportSpec
	for i, se := range exportList.Items {
		exportSpec := &mcs.ExportSpec{}
		err := exportSpec.UnmarshalObjectMeta(&se.ObjectMeta)
		if err != nil {
			return nil, err
		}
		if i == 0 || exportSpec.IsPreferredOver(primary) {
			primary = exportSpec
		}
	}
	return primary, nil
}

// create or update the ServiceImport corresponding to the provided export.
func (r *ServiceExportReconciler) ensureImportFor(ctx context.Context, se *mcsv1a1.ServiceExport) error {
	es := &mcs.ExportSpec{}

	err := es.UnmarshalObjectMeta(&se.ObjectMeta)
	if err != nil {
		return err
	}

	namespacedName := types.NamespacedName{
		Namespace: se.Namespace,
		Name:      se.Name,
	}
	log := r.Log.WithValues("service", namespacedName.Name)

	si := mcsv1a1.ServiceImport{}
	expected := mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespacedName.Namespace,
			Name:      namespacedName.Name,
		},
		Spec: mcsv1a1.ServiceImportSpec{
			Type:  es.Service.Type,
			Ports: es.Service.Ports,
		},
		Status: mcsv1a1.ServiceImportStatus{
			// not sure this is the way to update - it's seems like a cluster related field, some kind of a queue of all
			// the clusters with this service, but here we create si for each se, and se is only of one cluster.
			// each time it should contain only one cluster (the se cluster?)
			Clusters: []mcsv1a1.ClusterStatus{{Cluster: lhutil.GetOriginalObjectCluster(se.ObjectMeta)}},
		},
	}
	err = r.Client.Get(ctx, namespacedName, &si)
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = r.Client.Create(ctx, &expected)
			if err != nil {
				log.Error(err, "unable to create the needed Service Import")
			}
			return err
		}
		log.Error(err, "Failed to get the needed service import")
		return err
	}
	err = r.updateServiceImport(ctx, &si, &expected, log)
	return err
}

// update the given ServiceImport to match the expected serviceImport
func (r *ServiceExportReconciler) updateServiceImport(ctx context.Context, si, expected *mcsv1a1.ServiceImport, log logr.Logger) error {
	si.Spec.Type = expected.Spec.Type
	for i, expectedPort := range expected.Spec.Ports {
		if i < len(si.Spec.Ports) {
			expectedPort.DeepCopyInto(&si.Spec.Ports[i])
		} else {
			si.Spec.Ports = append(si.Spec.Ports, expectedPort)
		}
	}

	err := r.Client.Update(ctx, si)
	if err != nil {
		return err
	}
	return nil
}

// allow the object to be deleted by removing the finalizer.
// Once all finalizers have been removed, the ServiceExport object and its
// owned ServiceImport object will be deleted.
//
// Note: ownership will need to be reconsidered if merging imports
func (r *ServiceExportReconciler) handleDelete(ctx context.Context, se *mcsv1a1.ServiceExport) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(se, serviceExportFinalizerName) {
		// @todo: add the NamespacedName of the ServiceExport to be compatible with Reconcile?
		log := r.Log.WithValues("service", lhutil.GetOriginalObjectName(se.ObjectMeta)).
			WithValues("cluster", lhutil.GetOriginalObjectCluster(se.ObjectMeta))

		log.Info("Removing service export finalizer")

		// clear the finalizer
		controllerutil.RemoveFinalizer(se, serviceExportFinalizerName)
		if err := r.Client.Update(ctx, se); err != nil {
			// @todo handle version mismatch error?
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the reconciler with the manager.
func (r *ServiceExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mcsv1a1.ServiceExport{}).
		Owns(&mcsv1a1.ServiceImport{}).
		Complete(r)
}
