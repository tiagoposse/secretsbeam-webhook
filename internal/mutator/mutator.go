package mutator

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/segmentio/fasthash/fnv1a"
	"github.com/tiagoposse/kscp-webhook/internal/config"
	v1 "k8s.io/api/core/v1"
)

func MutateVolumes(provider string, volMounts []v1.VolumeMount, pod *v1.Pod, paths []string) []string {
	for _, volMount := range volMounts {
		if !slices.Contains(paths, volMount.MountPath) {
			paths = append(paths, volMount.MountPath)

			for index := range pod.Spec.Containers {
				pod.Spec.Containers[index].VolumeMounts = append(pod.Spec.Containers[index].VolumeMounts, volMount)
			}

			pod.Spec.Volumes = append(pod.Spec.Volumes, v1.Volume{
				Name: volMount.Name,
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			})
		}
	}

	return paths
}

func ParseProvider(provider string, pod *v1.Pod, secrets map[string]*config.SecretConfig) (*v1.Container, error) {
	var image string

	switch provider {
	case "aws":
		image = os.Getenv("AWS_AGENT_IMAGE")
	case "gcp":
		image = os.Getenv("GCP_AGENT_IMAGE")
	case "azure":
		image = os.Getenv("AZURE_AGENT_IMAGE")
	default:
		image = provider
	}

	data, err := json.Marshal(secrets)
	if err != nil {
		return nil, fmt.Errorf("marshalling secrets: %w", err)
	}

	cfg := base64.StdEncoding.EncodeToString(data)

	initContainer := &v1.Container{
		Name:            fmt.Sprintf("secret-injector-%s", provider),
		Image:           image,
		ImagePullPolicy: v1.PullIfNotPresent,
		Command: []string{
			"/agent",
		},
		Args: []string{
			"--config", cfg,
		},
	}
	existingPaths := make([]string, 0)

	i := 0
	providerHash := fnv1a.HashString64(provider)
	for secretName := range secrets {
		secrets[secretName] = extractSecretConfig(secretName, pod.Annotations)
		targetDir := filepath.Dir(secrets[secretName].Target)

		if !slices.Contains(existingPaths, targetDir) {
			existingPaths = append(existingPaths, targetDir)
			initContainer.VolumeMounts = append(initContainer.VolumeMounts, v1.VolumeMount{
				Name:      fmt.Sprintf("kscp-%d-%d", providerHash, i),
				MountPath: targetDir,
			})
			i++
		}
	}
	return initContainer, nil
}

func extractSecretConfig(secretName string, podAnnotations map[string]string) *config.SecretConfig {
	templateAnnotation := strings.ReplaceAll(__INJECTION_TEMPLATE_ANNOTATION, "{NAME}", secretName)
	targetAnnotation := strings.ReplaceAll(__INJECTION_TARGET_ANNOTATION, "{NAME}", secretName)

	cfg := &config.SecretConfig{}
	if val, ok := podAnnotations[templateAnnotation]; ok {
		cfg.Template = val
	}

	if val, ok := podAnnotations[targetAnnotation]; ok {
		cfg.Target = val
	} else {
		cfg.Target = fmt.Sprintf("/var/run/secrets/kscp.io/%s", secretName)
	}

	return cfg
}
