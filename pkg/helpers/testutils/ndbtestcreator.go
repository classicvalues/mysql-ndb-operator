// Copyright (c) 2021, Oracle and/or its affiliates.
//
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

package testutils

import (
	ndbcontroller "github.com/mysql/ndb-operator/pkg/apis/ndbcontroller/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewTestNdb creates a new NdbCluster resource with few preset for testing
func NewTestNdb(namespace string, name string, noOfNodes int32) *ndbcontroller.NdbCluster {
	return &ndbcontroller.NdbCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NdbCluster",
			APIVersion: ndbcontroller.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: ndbcontroller.NdbClusterSpec{
			NodeCount:       noOfNodes,
			RedundancyLevel: 2,
			Mysqld: &ndbcontroller.NdbMysqldSpec{
				NodeCount: noOfNodes,
			},
		},
	}
}
