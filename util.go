package main

import (
	"strings"

	core "k8s.io/api/core/v1"
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

func findN(n []core.Namespace, s string) (string, bool) {
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
