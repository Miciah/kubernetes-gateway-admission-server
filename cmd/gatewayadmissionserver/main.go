package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"k8s.io/klog/v2"

	admissionv1 "k8s.io/api/admission/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientset "k8s.io/client-go/kubernetes"
	authorizationclient "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"

	"github.com/openshift/generic-admission-server/pkg/cmd"
)

func main() {
	cmd.RunAdmissionServer(&admissionHook{})
}

type admissionHook struct {
	sarClient authorizationclient.SubjectAccessReviewInterface

	lock        sync.RWMutex
	initialized bool
}

func (a *admissionHook) ValidatingResource() (plural schema.GroupVersionResource, singular string) {
	return schema.GroupVersionResource{
			Group:    "admission.networking.x-k8s.io",
			Version:  "v1alpha2",
			Resource: "gateways",
		},
		"gateway"
}

func (a *admissionHook) Validate(admissionSpec *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	klog.Infof("got AdmissionRequest: %#v", admissionSpec)
	if admissionSpec.Operation != admissionv1.Create && admissionSpec.Operation != admissionv1.Update {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}
	if len(admissionSpec.SubResource) != 0 {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}
	if admissionSpec.Resource.Group != "networking.x-k8s.io" || admissionSpec.Resource.Resource != "gateways" {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	gateway := &Gateway{}
	if err := json.Unmarshal(admissionSpec.Object.Raw, gateway); err != nil {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
				Reason:  metav1.StatusReasonBadRequest,
				Status:  metav1.StatusFailure,
			},
		}
	}
	if len(gateway.Spec.GatewayClassName) == 0 {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusForbidden,
				Message: "gatewayClassName is required",
				Reason:  metav1.StatusReasonForbidden,
				Status:  metav1.StatusFailure,
			},
		}
	}
	if admissionSpec.Operation == admissionv1.Update {
		oldGateway := &Gateway{}
		err := json.Unmarshal(admissionSpec.OldObject.Raw, gateway)
		if err != nil {
			return &admissionv1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Code:    http.StatusBadRequest,
					Message: err.Error(),
					Reason:  metav1.StatusReasonBadRequest,
					Status:  metav1.StatusFailure,
				},
			}
		}
		if gateway.Spec.GatewayClassName != oldGateway.Spec.GatewayClassName {
			return &admissionv1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Code:    http.StatusForbidden,
					Message: "gatewayClassName is immutable",
					Reason:  metav1.StatusReasonForbidden,
					Status:  metav1.StatusFailure,
				},
			}
		}
	}

	a.lock.RLock()
	defer a.lock.RUnlock()
	if !a.initialized {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusInternalServerError,
				Message: "not initialized",
				Reason:  metav1.StatusReasonInternalError,
				Status:  metav1.StatusFailure,
			},
		}
	}
	var extra map[string]authorizationv1.ExtraValue
	if admissionSpec.UserInfo.Extra != nil {
		extra := map[string]authorizationv1.ExtraValue{}
		for k, v := range admissionSpec.UserInfo.Extra {
			extra[k] = authorizationv1.ExtraValue(v)
		}
	}
	request := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   admissionSpec.UserInfo.Username,
			UID:    admissionSpec.UserInfo.UID,
			Groups: admissionSpec.UserInfo.Groups,
			Extra:  extra,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:    "networking.x-k8s.io",
				Name:     gateway.Spec.GatewayClassName,
				Resource: "gatewayclasses",
				Verb:     "use",
				Version:  "v1alpha1",
			},
		},
	}
	if result, err := a.sarClient.Create(context.TODO(), request, metav1.CreateOptions{}); err != nil {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
				Reason:  metav1.StatusReasonInternalError,
				Status:  metav1.StatusFailure,
			},
		}
	} else if result.Status.Allowed {
		return &admissionv1.AdmissionResponse{Allowed: true}
	} else if result.Status.Denied {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusForbidden,
				Message: result.Status.Reason,
				Reason:  metav1.StatusReasonForbidden,
				Status:  metav1.StatusFailure,
			},
		}
	} else {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusInternalServerError,
				Message: result.Status.Reason,
				Reason:  metav1.StatusReasonInternalError,
				Status:  metav1.StatusFailure,
			},
		}
	}
}

func (a *admissionHook) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	kubeClient := clientset.NewForConfigOrDie(kubeClientConfig)
	a.sarClient = kubeClient.AuthorizationV1().SubjectAccessReviews()
	a.initialized = true

	return nil
}

type Gateway struct {
	Spec GatewaySpec `json:"spec"`
}

type GatewaySpec struct {
	GatewayClassName string `json:"gatewayClassName"`
}
