// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"time"

	"carvel.dev/kapp-controller/pkg/apis/kappctrl/v1alpha1"
	"carvel.dev/kapp-controller/pkg/exec"
	"carvel.dev/kapp-controller/pkg/memdir"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconcile is not expected to be called concurrently
func (a *App) Reconcile(force bool) (reconcile.Result, error) {
	defer a.flushUpdateStatus("app reconciled")

	var err error

	a.appMetrics.ReconcileCountMetrics.InitMetrics(a.Kind(), a.Name(), a.Namespace())

	timerOpts := ReconcileTimerOpts{
		DefaultSyncPeriod: a.opts.DefaultSyncPeriod,
		MinimumSyncPeriod: a.opts.MinimumSyncPeriod,
	}

	switch {
	case a.app.DeletionTimestamp != nil:
		a.log.Info("Started delete")
		defer func() { a.log.Info("Completed delete") }()

		err = a.reconcileDelete()

	case a.app.Spec.Canceled || a.app.Spec.Paused:
		a.log.Info("App is canceled or paused, not reconciling")

		a.markObservedLatest()
		a.app.Status.FriendlyDescription = "Canceled/paused"

		err = a.updateStatus("app canceled/paused")

	case force || NewReconcileTimer(a.app, timerOpts).IsReadyAt(time.Now()):
		a.log.Info("Started deploy")
		defer func() { a.log.Info("Completed deploy") }()

		err = a.reconcileDeploy()

	default:
		a.log.Info("Reconcile noop")
	}

	return reconcile.Result{RequeueAfter: NewReconcileTimer(a.app, timerOpts).DurationUntilReady(err)}, err
}

func (a *App) reconcileDelete() error {
	a.markObservedLatest()
	a.setDeleting()

	vendir := a.fetchFactory.NewVendir(a.app.Namespace)
	err := vendir.ClearCache(a.cacheID())
	if err != nil {
		return err
	}

	err = a.updateStatus("marking deleting")
	if err != nil {
		return err
	}

	a.resetLastDeployStartedAt()

	result := a.delete(a.updateLastDeployNoReturn)
	a.setDeleteCompleted(result)

	// Resource is gone so this will error, ignore it
	_ = a.updateStatus("marking delete completed")
	return nil
}

func (a *App) reconcileDeploy() error {
	a.markObservedLatest()
	a.setReconciling()

	err := a.updateStatus("marking reconciling")
	if err != nil {
		return err
	}

	result := a.reconcileFetchTemplateDeploy()
	a.setReconcileCompleted(result)

	// Reconcile inspect regardless of deploy success
	// but don't inspect if deploy never attempted
	if a.app.Status.Deploy != nil {
		_ = a.reconcileInspect()
	}

	return a.updateStatus("marking reconcile completed")
}

func (a *App) reconcileFetchTemplateDeploy() exec.CmdRunResult {
	reconcileStartTime := time.Now()
	a.appMetrics.IsFirstReconcile = a.appMetrics.ReconcileCountMetrics.GetReconcileAttemptCounterValue(a.Kind(), a.Name(), a.Namespace()) == 1
	defer func() {
		a.appMetrics.ReconcileTimeMetrics.RegisterOverallTime(a.Kind(), a.Name(), a.Namespace(), a.appMetrics.IsFirstReconcile,
			time.Since(reconcileStartTime))
	}()

	tmpDir := memdir.NewTmpDir("fetch-template-deploy")

	err := tmpDir.Create()
	if err != nil {
		return exec.NewCmdRunResultWithErr(err)
	}

	defer tmpDir.Remove()

	assetsPath := tmpDir.Path()

	{
		a.resetLastFetchStartedAt()

		var fetchResult exec.CmdRunResult
		assetsPath, fetchResult = a.fetch(assetsPath)

		a.app.Status.Fetch = &v1alpha1.AppStatusFetch{
			Stderr:    fetchResult.Stderr,
			Stdout:    fetchResult.Stdout,
			ExitCode:  fetchResult.ExitCode,
			Error:     fetchResult.ErrorStr(),
			StartedAt: a.app.Status.Fetch.StartedAt,
			UpdatedAt: metav1.NewTime(time.Now().UTC()),
		}

		a.appMetrics.ReconcileTimeMetrics.RegisterFetchTime(a.Kind(), a.Name(), a.Namespace(), a.appMetrics.IsFirstReconcile,
			a.app.Status.Fetch.UpdatedAt.Sub(a.app.Status.Fetch.StartedAt.Time))

		err := a.updateStatus("marking fetch completed")
		if err != nil {
			return exec.NewCmdRunResultWithErr(err)
		}

		if fetchResult.Error != nil {
			return fetchResult
		}
	}

	templateStartTime := time.Now()

	tplResult := a.template(assetsPath)

	a.app.Status.Template = &v1alpha1.AppStatusTemplate{
		Stderr:    tplResult.Stderr,
		ExitCode:  tplResult.ExitCode,
		Error:     tplResult.ErrorStr(),
		UpdatedAt: metav1.NewTime(time.Now().UTC()),
	}

	a.appMetrics.ReconcileTimeMetrics.RegisterTemplateTime(a.Kind(), a.Name(), a.Namespace(), a.appMetrics.IsFirstReconcile,
		a.app.Status.Template.UpdatedAt.Sub(templateStartTime))

	err = a.updateStatus("marking template completed")
	if err != nil {
		return exec.NewCmdRunResultWithErr(err)
	}

	if tplResult.Error != nil {
		return tplResult
	}

	a.resetLastDeployStartedAt()

	return a.updateLastDeploy(a.deploy(tplResult.Stdout, a.updateLastDeployNoReturn))
}

func (a *App) updateLastDeploy(result exec.CmdRunResult) exec.CmdRunResult {
	result = result.WithFriendlyYAMLStrings()

	a.app.Status.Deploy = &v1alpha1.AppStatusDeploy{
		Stdout:           result.Stdout,
		Stderr:           result.Stderr,
		Finished:         result.Finished,
		ExitCode:         result.ExitCode,
		Error:            result.ErrorStr(),
		StartedAt:        a.app.Status.Deploy.StartedAt,
		UpdatedAt:        metav1.NewTime(time.Now().UTC()),
		KappDeployStatus: a.app.Status.Deploy.KappDeployStatus,
	}

	defer a.updateStatus("marking last deploy")

	if a.metadata == nil {
		return result
	}

	// Do not overwrite kapp deploy status during delete
	if len(a.metadata.LastChange.Namespaces) == 0 {
		return result
	}

	usedGKs := []metav1.GroupKind{}
	for _, gk := range a.metadata.UsedGKs {
		usedGKs = append(usedGKs, metav1.GroupKind{
			gk.Group, gk.Kind,
		})
	}

	a.app.Status.Deploy.KappDeployStatus = &v1alpha1.KappDeployStatus{
		AssociatedResources: v1alpha1.AssociatedResources{
			Label:      fmt.Sprintf("%s=%s", a.metadata.LabelKey, a.metadata.LabelValue),
			Namespaces: a.metadata.LastChange.Namespaces,
			GroupKinds: usedGKs,
		},
	}

	a.appMetrics.ReconcileTimeMetrics.RegisterDeployTime(a.Kind(), a.Name(), a.Namespace(), a.appMetrics.IsFirstReconcile,
		a.Status().Deploy.UpdatedAt.Sub(a.Status().Deploy.StartedAt.Time))

	return result
}

func (a *App) updateLastDeployNoReturn(result exec.CmdRunResult) {
	a.updateLastDeploy(result)
}

func (a *App) resetLastFetchStartedAt() {
	if a.app.Status.Fetch == nil {
		a.app.Status.Fetch = &v1alpha1.AppStatusFetch{}
	}
	a.app.Status.Fetch.StartedAt = metav1.NewTime(time.Now().UTC())
}

func (a *App) resetLastDeployStartedAt() {
	if a.app.Status.Deploy == nil {
		a.app.Status.Deploy = &v1alpha1.AppStatusDeploy{}
	}
	a.app.Status.Deploy.StartedAt = metav1.NewTime(time.Now().UTC())
}

func (a *App) reconcileInspect() error {
	inspectResult := a.inspect().WithFriendlyYAMLStrings()

	if !inspectResult.IsEmpty() {
		a.app.Status.Inspect = &v1alpha1.AppStatusInspect{
			Stdout:    inspectResult.Stdout,
			Stderr:    inspectResult.Stderr,
			ExitCode:  inspectResult.ExitCode,
			Error:     inspectResult.ErrorStr(),
			UpdatedAt: metav1.NewTime(time.Now().UTC()),
		}
	} else {
		a.app.Status.Inspect = nil
	}

	return a.updateStatus("marking inspect completed")
}

func (a *App) markObservedLatest() {
	a.app.Status.ObservedGeneration = a.app.Generation
}

func (a *App) setReconciling() {
	a.removeAllConditions()

	a.app.Status.Conditions = append(a.app.Status.Conditions, v1alpha1.Condition{
		Type:   v1alpha1.Reconciling,
		Status: corev1.ConditionTrue,
	})

	a.appMetrics.ReconcileCountMetrics.RegisterReconcileAttempt(a.Kind(), a.Name(), a.Namespace())
	a.app.Status.FriendlyDescription = "Reconciling"
}

func (a *App) setReconcileCompleted(result exec.CmdRunResult) {
	a.removeAllConditions()

	if result.Error != nil {
		a.app.Status.Conditions = append(a.app.Status.Conditions, v1alpha1.Condition{
			Type:    v1alpha1.ReconcileFailed,
			Status:  corev1.ConditionTrue,
			Message: result.ErrorStr(),
		})
		a.app.Status.ConsecutiveReconcileFailures++
		a.app.Status.ConsecutiveReconcileSuccesses = 0
		a.app.Status.FriendlyDescription = fmt.Sprintf("Reconcile failed: %s", result.ErrorStr())
		a.appMetrics.ReconcileCountMetrics.RegisterReconcileFailure(a.Kind(), a.Name(), a.Namespace())
		a.setUsefulErrorMessage(result)
	} else {
		a.app.Status.Conditions = append(a.app.Status.Conditions, v1alpha1.Condition{
			Type:    v1alpha1.ReconcileSucceeded,
			Status:  corev1.ConditionTrue,
			Message: "",
		})
		a.app.Status.ConsecutiveReconcileSuccesses++
		a.app.Status.ConsecutiveReconcileFailures = 0
		a.app.Status.FriendlyDescription = "Reconcile succeeded"
		a.appMetrics.ReconcileCountMetrics.RegisterReconcileSuccess(a.Kind(), a.Name(), a.Namespace())
		a.app.Status.UsefulErrorMessage = ""
	}
}

func (a *App) setDeleting() {
	a.removeAllConditions()

	a.app.Status.Conditions = append(a.app.Status.Conditions, v1alpha1.Condition{
		Type:   v1alpha1.Deleting,
		Status: corev1.ConditionTrue,
	})

	a.appMetrics.ReconcileCountMetrics.RegisterReconcileDeleteAttempt(a.Kind(), a.Name(), a.Namespace())
	a.app.Status.FriendlyDescription = "Deleting"
}

func (a *App) setDeleteCompleted(result exec.CmdRunResult) {
	a.removeAllConditions()

	if result.Error != nil {
		a.app.Status.Conditions = append(a.app.Status.Conditions, v1alpha1.Condition{
			Type:    v1alpha1.DeleteFailed,
			Status:  corev1.ConditionTrue,
			Message: result.ErrorStr(),
		})
		a.app.Status.ConsecutiveReconcileFailures++
		a.app.Status.ConsecutiveReconcileSuccesses = 0
		a.app.Status.FriendlyDescription = fmt.Sprintf("Delete failed: %s", result.ErrorStr())
		a.appMetrics.ReconcileCountMetrics.RegisterReconcileDeleteFailed(a.Kind(), a.Name(), a.Namespace())
		a.setUsefulErrorMessage(result)
	} else {
		a.appMetrics.ReconcileCountMetrics.DeleteMetrics(a.Kind(), a.Name(), a.Namespace())
	}
}

func (a *App) removeAllConditions() {
	a.app.Status.Conditions = nil
}

func (a *App) setUsefulErrorMessage(result exec.CmdRunResult) {
	switch {
	case result.Stderr != "":
		a.app.Status.UsefulErrorMessage = result.Stderr
	default:
		a.app.Status.UsefulErrorMessage = result.ErrorStr()
	}
}
