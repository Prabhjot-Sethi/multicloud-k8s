package resourcebundlestate

import (
	"context"
	"log"

	"github.com/onap/multicloud-k8s/src/monitor/pkg/apis/k8splugin/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// AddStatefulSetController the new controller to the controller manager
func AddStatefulSetController(mgr manager.Manager) error {
	return addStatefulSetController(mgr, newStatefulSetReconciler(mgr))
}

func addStatefulSetController(mgr manager.Manager, r *statefulSetReconciler) error {
	// Create a new controller
	c, err := controller.New("Statefulset-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to secondar resource StatefulSets
	// Predicate filters StatefulSet which don't have the k8splugin label
	err = c.Watch(&source.Kind{Type: &appsv1.StatefulSet{}}, &handler.EnqueueRequestForObject{}, &statefulSetPredicate{})
	if err != nil {
		return err
	}

	return nil
}

func newStatefulSetReconciler(m manager.Manager) *statefulSetReconciler {
	return &statefulSetReconciler{client: m.GetClient()}
}

type statefulSetReconciler struct {
	client client.Client
}

// Reconcile implements the loop that will update the ResourceBundleState CR
// whenever we get any updates from all the StatefulSets we watch.
func (r *statefulSetReconciler) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	log.Printf("Updating ResourceBundleState for StatefulSet: %+v\n", req)

	sfs := &appsv1.StatefulSet{}
	err := r.client.Get(context.TODO(), req.NamespacedName, sfs)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Printf("StatefulSet not found: %+v. Remove from CR if it is stored there.\n", req.NamespacedName)
			// Remove the StatefulSet's status from StatusList
			// This can happen if we get the DeletionTimeStamp event
			// after the StatefulSet has been deleted.
			r.deleteStatefulSetFromAllCRs(req.NamespacedName)
			return reconcile.Result{}, nil
		}
		log.Printf("Failed to get statefulSet: %+v\n", req.NamespacedName)
		return reconcile.Result{}, err
	}

	// Find the CRs which track this statefulSet via the labelselector
	crSelector := returnLabel(sfs.GetLabels())
	if crSelector == nil {
		log.Println("We should not be here. The predicate should have filtered this StatefulSet")
	}

	// Get the CRs which have this label and update them all
	// Ideally, we will have only one CR, but there is nothing
	// preventing the creation of multiple.
	// TODO: Consider using an admission validating webook to prevent multiple
	rbStatusList := &v1alpha1.ResourceBundleStateList{}
	err = listResources(r.client, req.Namespace, crSelector, rbStatusList)
	if err != nil || len(rbStatusList.Items) == 0 {
		log.Printf("Did not find any CRs tracking this resource\n")
		return reconcile.Result{}, nil
	}

	err = r.updateCRs(rbStatusList, sfs)
	if err != nil {
		// Requeue the update
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// deleteStatefulSetFromAllCRs deletes statefulSet status from all the CRs when the StatefulSet itself has been deleted
// and we have not handled the updateCRs yet.
// Since, we don't have the statefulSet's labels, we need to look at all the CRs in this namespace
func (r *statefulSetReconciler) deleteStatefulSetFromAllCRs(namespacedName types.NamespacedName) error {

	rbStatusList := &v1alpha1.ResourceBundleStateList{}
	err := listResources(r.client, namespacedName.Namespace, nil, rbStatusList)
	if err != nil || len(rbStatusList.Items) == 0 {
		log.Printf("Did not find any CRs tracking this resource\n")
		return nil
	}
	for _, cr := range rbStatusList.Items {
		r.deleteFromSingleCR(&cr, namespacedName.Name)
	}

	return nil
}

func (r *statefulSetReconciler) updateCRs(crl *v1alpha1.ResourceBundleStateList, sfs *appsv1.StatefulSet) error {

	for _, cr := range crl.Items {
		// StatefulSet is not scheduled for deletion
		if sfs.DeletionTimestamp == nil {
			err := r.updateSingleCR(&cr, sfs)
			if err != nil {
				return err
			}
		} else {
			// StatefulSet is scheduled for deletion
			r.deleteFromSingleCR(&cr, sfs.Name)
		}
	}

	return nil
}

func (r *statefulSetReconciler) deleteFromSingleCR(cr *v1alpha1.ResourceBundleState, name string) error {
	cr.Status.ResourceCount--
	length := len(cr.Status.StatefulSetStatuses)
	for i, rstatus := range cr.Status.StatefulSetStatuses {
		if rstatus.Name == name {
			//Delete that status from the array
			cr.Status.StatefulSetStatuses[i] = cr.Status.StatefulSetStatuses[length-1]
			cr.Status.StatefulSetStatuses[length-1].Status = appsv1.StatefulSetStatus{}
			cr.Status.StatefulSetStatuses = cr.Status.StatefulSetStatuses[:length-1]
			return nil
		}
	}

	log.Println("Did not find a status for StatefulSet in CR")
	return nil
}

func (r *statefulSetReconciler) updateSingleCR(cr *v1alpha1.ResourceBundleState, sfs *appsv1.StatefulSet) error {

	// Update status after searching for it in the list of resourceStatuses
	for i, rstatus := range cr.Status.StatefulSetStatuses {
		// Look for the status if we already have it in the CR
		if rstatus.Name == sfs.Name {
			sfs.Status.DeepCopyInto(&cr.Status.StatefulSetStatuses[i].Status)
			err := r.client.Status().Update(context.TODO(), cr)
			if err != nil {
				log.Printf("failed to update rbstate: %v\n", err)
				return err
			}
			return nil
		}
	}

	// Exited for loop with no status found
	// Increment the number of tracked resources
	cr.Status.ResourceCount++

	// Add it to CR
	cr.Status.StatefulSetStatuses = append(cr.Status.StatefulSetStatuses, appsv1.StatefulSet{
		TypeMeta:   sfs.TypeMeta,
		ObjectMeta: sfs.ObjectMeta,
		Status:     sfs.Status,
	})

	err := r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		log.Printf("failed to update rbstate: %v\n", err)
		return err
	}

	return nil
}
