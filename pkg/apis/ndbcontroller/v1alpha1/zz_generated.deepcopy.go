// +build !ignore_autogenerated

// Copyright (c) 2021, Oracle and/or its affiliates.
//
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

// Code generated by deepcopy-gen. DO NOT EDIT.

package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Ndb) DeepCopyInto(out *Ndb) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Ndb.
func (in *Ndb) DeepCopy() *Ndb {
	if in == nil {
		return nil
	}
	out := new(Ndb)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *Ndb) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NdbList) DeepCopyInto(out *NdbList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Ndb, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NdbList.
func (in *NdbList) DeepCopy() *NdbList {
	if in == nil {
		return nil
	}
	out := new(NdbList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *NdbList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NdbMysqldSpec) DeepCopyInto(out *NdbMysqldSpec) {
	*out = *in
	if in.RootPasswordSecretName != nil {
		in, out := &in.RootPasswordSecretName, &out.RootPasswordSecretName
		*out = new(string)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NdbMysqldSpec.
func (in *NdbMysqldSpec) DeepCopy() *NdbMysqldSpec {
	if in == nil {
		return nil
	}
	out := new(NdbMysqldSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NdbSpec) DeepCopyInto(out *NdbSpec) {
	*out = *in
	if in.RedundancyLevel != nil {
		in, out := &in.RedundancyLevel, &out.RedundancyLevel
		*out = new(int32)
		**out = **in
	}
	if in.NodeCount != nil {
		in, out := &in.NodeCount, &out.NodeCount
		*out = new(int32)
		**out = **in
	}
	if in.DataNodePVCSpec != nil {
		in, out := &in.DataNodePVCSpec, &out.DataNodePVCSpec
		*out = new(v1.PersistentVolumeClaimSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Mysqld != nil {
		in, out := &in.Mysqld, &out.Mysqld
		*out = new(NdbMysqldSpec)
		(*in).DeepCopyInto(*out)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NdbSpec.
func (in *NdbSpec) DeepCopy() *NdbSpec {
	if in == nil {
		return nil
	}
	out := new(NdbSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NdbStatus) DeepCopyInto(out *NdbStatus) {
	*out = *in
	in.LastUpdate.DeepCopyInto(&out.LastUpdate)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NdbStatus.
func (in *NdbStatus) DeepCopy() *NdbStatus {
	if in == nil {
		return nil
	}
	out := new(NdbStatus)
	in.DeepCopyInto(out)
	return out
}
