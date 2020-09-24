module kubernetes-gateway-admission-server

go 1.15

require (
	github.com/openshift/generic-admission-server v1.14.1-0.20200903115324-4ddcdd976480
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/apiserver v0.19.2 // indirect
	k8s.io/client-go v0.19.2
	k8s.io/klog/v2 v2.3.0
)
