/*
Copyright 2024.

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

package e2e

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/akyriako/typesense-operator/test/utils"
)

const namespace = "typesense-operator-system"

const (
	clusterSampleFile  = "config/samples/ts_v1alpha1_typesensecluster.yaml"
	clusterName        = "cluster-1"
	clusterServiceFQDN = clusterName + "-svc." + namespace + ".svc.cluster.local"

	apiKeySampleFile = "config/samples/ts_v1alpha1_typesenseapikey.yaml"
	apiKeyName       = "search-only-key"
)

// runCurlPod runs script (a shell one-liner) to completion in a throwaway curlimages/curl pod
// and returns its stdout. It waits for the pod to reach Succeeded and reads via `kubectl logs`
// rather than `kubectl run --rm -i` attach output, since attach races the pod completing for
// short-lived commands and can silently return kubectl's own status messages instead of the
// container's output.
func runCurlPod(podName string, script string) (string, error) {
	_, _ = utils.Run(exec.Command("kubectl", "delete", "pod", podName, "-n", namespace, "--ignore-not-found"))
	defer func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "pod", podName, "-n", namespace, "--ignore-not-found"))
	}()

	cmd := exec.Command("kubectl", "run", podName,
		"-n", namespace,
		"--image=curlimages/curl",
		"--restart=Never",
		"--command", "--",
		"sh", "-c", script,
	)
	if _, err := utils.Run(cmd); err != nil {
		return "", err
	}

	cmd = exec.Command("kubectl", "wait", "pod/"+podName,
		"-n", namespace,
		"--for", "jsonpath={.status.phase}=Succeeded",
		"--timeout", "30s",
	)
	if _, err := utils.Run(cmd); err != nil {
		return "", err
	}

	out, err := utils.Run(exec.Command("kubectl", "logs", podName, "-n", namespace))
	return string(out), err
}

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {
		By("installing prometheus operator")
		Expect(utils.InstallPrometheusOperator()).To(Succeed())

		By("installing the cert-manager")
		Expect(utils.InstallCertManager()).To(Succeed())

		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		By("uninstalling the Prometheus manager bundle")
		utils.UninstallPrometheusOperator()

		By("uninstalling the cert-manager bundle")
		utils.UninstallCertManager()

		By("removing manager namespace")
		cmd := exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			var controllerPodName string
			var err error

			// projectimage stores the name of the image used in the example
			var projectimage = "example.com/typesense-operator:v0.0.1"

			By("building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("loading the the manager(Operator) image on Kind")
			err = utils.LoadImageToKindClusterWithName(projectimage)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("installing CRDs")
			cmd = exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("deploying the controller-manager")
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Get pod name

				cmd = exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
				}
				controllerPodName = podNames[0]
				ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))

				// Validate pod status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				status, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if string(status) != "Running" {
					return fmt.Errorf("controller pod in %s status", status)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyControllerUp, time.Minute, time.Second).Should(Succeed())

		})
	})

	Context("APIKey lifecycle", func() {
		AfterAll(func() {
			By("removing the api key sample")
			cmd := exec.Command("kubectl", "delete", "-n", namespace, "-f", apiKeySampleFile, "--ignore-not-found")
			_, _ = utils.Run(cmd)

			By("removing the typesense cluster sample")
			cmd = exec.Command("kubectl", "delete", "-n", namespace, "-f", clusterSampleFile, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should stand up a real Typesense cluster and issue a correctly scoped api key", func() {
			By("applying the typesense cluster sample")
			cmd := exec.Command("kubectl", "apply", "-n", namespace, "-f", clusterSampleFile)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the typesense cluster to reach quorum")
			cmd = exec.Command("kubectl", "wait", fmt.Sprintf("typesensecluster/%s", clusterName),
				"-n", namespace,
				"--for", "condition=Ready",
				"--timeout", "8m",
			)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("applying the api key sample")
			cmd = exec.Command("kubectl", "apply", "-n", namespace, "-f", apiKeySampleFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the api key to be reconciled")
			cmd = exec.Command("kubectl", "wait", fmt.Sprintf("typesenseapikey/%s", apiKeyName),
				"-n", namespace,
				"--for", "condition=Ready",
				"--timeout", "2m",
			)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("fetching the secret holding the generated api key")
			cmd = exec.Command("kubectl", "get", fmt.Sprintf("typesenseapikey/%s", apiKeyName),
				"-n", namespace,
				"-o", "jsonpath={.status.secretRef.name}",
			)
			secretNameOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			secretName := strings.TrimSpace(string(secretNameOutput))
			Expect(secretName).NotTo(BeEmpty())

			cmd = exec.Command("kubectl", "get", "secret", secretName,
				"-n", namespace,
				"-o", "jsonpath={.data.value}",
			)
			encodedKeyOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			decodedKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encodedKeyOutput)))
			Expect(err).NotTo(HaveOccurred())
			apiKeyValue := string(decodedKey)
			Expect(apiKeyValue).NotTo(BeEmpty())

			By("verifying the api key authenticates against the real typesense cluster")
			searchUrl := fmt.Sprintf("http://%s:8108/collections/nonexistent-collection/documents/search?q=*&query_by=name", clusterServiceFQDN)
			searchScript := fmt.Sprintf("echo STATUS:$(curl -s -o /dev/null -w '%%{http_code}' -H 'X-TYPESENSE-API-KEY: %s' '%s')",
				apiKeyValue, searchUrl)
			searchOutput, err := runCurlPod("tsapikey-verify-search", searchScript)
			Expect(err).NotTo(HaveOccurred())
			Expect(searchOutput).To(ContainSubstring("STATUS:404"),
				"expected the generated key to authenticate (404 collection-not-found), not be rejected")

			By("verifying the api key is scoped to documents:search only")
			collectionsUrl := fmt.Sprintf("http://%s:8108/collections", clusterServiceFQDN)
			scopeScript := fmt.Sprintf("echo STATUS:$(curl -s -o /dev/null -w '%%{http_code}' -X POST -H 'X-TYPESENSE-API-KEY: %s' -H 'Content-Type: application/json' -d '{\"name\":\"should-not-be-created\",\"fields\":[]}' '%s')",
				apiKeyValue, collectionsUrl)
			scopeOutput, err := runCurlPod("tsapikey-verify-scope", scopeScript)
			Expect(err).NotTo(HaveOccurred())
			Expect(scopeOutput).To(ContainSubstring("STATUS:401"),
				"expected the search-only key to be rejected for collections:create")
		})
	})
})
