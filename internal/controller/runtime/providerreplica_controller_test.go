/*
Copyright 2025.

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

package runtime

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
)

var _ = Describe("ProviderReplica Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		providerreplica := &runtimev1alpha1.ProviderReplica{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ProviderReplica")
			err := k8sClient.Get(ctx, typeNamespacedName, providerreplica)
			if err != nil && errors.IsNotFound(err) {
				resource := &runtimev1alpha1.ProviderReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
						Annotations: map[string]string{
							runtimev1alpha1.ProviderReplicaGeneration: "1",
						},
					},
					Spec: runtimev1alpha1.ProviderReplicaSpec{
						HostID: "test-host",
						ProviderRef: corev1.ObjectReference{
							Name:      "test-provider",
							Namespace: "default",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &runtimev1alpha1.ProviderReplica{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ProviderReplica")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			// TODO(lxf): write the test
			// controllerReconciler := &ProviderReplicaReconciler{
			// 	Client: k8sClient,
			// 	Scheme: k8sClient.Scheme(),
			// }

			// _, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
			// 	NamespacedName: typeNamespacedName,
			// })
			// Expect(err).NotTo(HaveOccurred())
		})
	})
})
