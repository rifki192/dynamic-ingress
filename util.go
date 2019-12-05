package main

import (
	"reflect"
	"strings"

	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
)

func genDomain(n string) string {
	return strings.Replace(domainSuffix, "<namespace>", n, -1)
}

func findB(f []extensions.IngressBackend, s string) bool {
	_ = len(f)
	for _, v := range f {
		if v.ServiceName == s {
			return true
		}
	}

	return false
}

func findN(n []v1.Namespace, s string) (string, bool) {
	_ = len(n)
	for _, v := range n {
		lb := v.GetLabels()
		val, _ := lb["dynamic-ingress/auto"]
		if v.Name == s {
			return val, true
		}
	}

	return "", false
}

func findI(i []extensions.Ingress, name string, namespace string) bool {
	_ = len(i)
	for _, v := range i {
		if v.Name == name && v.Namespace == namespace {
			return true
		}
	}

	return false
}

func isEligibleForSync(obj interface{}) bool {
	switch obj.(type) {
	case *v1.Namespace:
		nms := obj.(*v1.Namespace)
		lb := nms.Labels
		log.Info("Check if Namespace ",nms.Name,", event is elligible for update")

		_, found := lb["dynamic-ingress/auto"]
		_, found2 := findN(cacheNamespaces, nms.Name)
		if found || found2 {
			return true
		}
	case *v1.Service:
		svc := obj.(*v1.Service)
		lb := svc.Labels
		log.Info("Check if Service ",svc.Name,", event is elligible for update")

		val, found := lb["dynamic-ingress/auto"]
		_, found2 := findN(cacheNamespaces, svc.Namespace)
		if found2 {
			return true
		}else if (found && val == "enabled"){
			return true
		}
	default:
		log.Info(reflect.TypeOf(obj))
		return false
	}

	return false
}
