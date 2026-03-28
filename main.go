package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"

	fledged "github.com/senthilrch/kube-fledged/pkg/apis/kubefledged/v1alpha2"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	certFile string
	keyFile  string
	port     int
	mutex    = &sync.Mutex{}
)

// imageUrl : podName/Namespace/containerName
var imagesMap = make(map[string][]string)

// var kubeClient *kubernetes.Clientset
var kubeClient client.Client

func init() {
	flag.StringVar(&certFile, "tls-cert", "/etc/webhooks/cdi-images-validator-certs/tls.crt", "TLS certificate file")
	flag.StringVar(&keyFile, "tls-key", "/etc/webhooks/cdi-images-validator-certs/tls.key", "TLS key file")
	flag.IntVar(&port, "port", 8443, "Webhook server port")
	flag.Parse()
}

func main() {

	log.Println("Starting webhook server...")

	defer func() {
		if err := recover(); err != nil {
			// this aoid process exit and debug the issue
			log.Println("panic occurred:", err)
		}
	}()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Printf("Error building kubeconfig: %s", err.Error())
		kubeClient = nil
	} else {
		kubeClient, err = client.New(cfg, client.Options{})
		if err != nil {
			log.Printf("Error building kubernetes clientset: %s", err.Error())
		}
	}

	http.HandleFunc("/validate-images", admitHandler)
	http.HandleFunc("/dump-images", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mutex.Lock()
		defer mutex.Unlock()
		respBody, err := json.MarshalIndent(imagesMap, "", "  ")
		if err != nil {
			http.Error(w, "failed to marshal images map", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(respBody)
		// if err := json.NewEncoder(w).Encode(imagesMap); err != nil {
		// 	http.Error(w, "failed to encode images map", http.StatusInternalServerError)
		// 	return
		// }
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	http.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("starting validating webhook server (image collection) on %s", addr)
	err = http.ListenAndServeTLS(addr, certFile, keyFile, nil)
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

func stopProcessing(w http.ResponseWriter, reviewReq *admissionv1.AdmissionReview, reason string) {

	log.Printf("Stopping processing with reason: %s", reason)

	reviewResp := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			Allowed: true,
			Warnings: []string{
				reason,
			},
		},
	}

	if reviewReq != nil {
		// must copy UID from request to response
		if reviewReq.Request != nil {
			reviewResp.Response.UID = reviewReq.Request.UID
		}
	}

	respBody, err := json.Marshal(reviewResp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBody)
}

// we should always return allowed=true. we just want to collect the images used in the cluster.
func admitHandler(w http.ResponseWriter, r *http.Request) {

	body, err := io.ReadAll(r.Body)
	if err != nil {
		stopProcessing(w, nil, "failed to read request body")
		return
	}

	var reviewReq admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &reviewReq); err != nil {
		stopProcessing(w, nil, "failed to unmarshal admission review")
		return
	}

	if r.Method != http.MethodPost {
		stopProcessing(w, &reviewReq, "only POST allowed but "+r.Method+" received")
		return
	}

	go handleAdmission(reviewReq.Request)

	stopProcessing(w, &reviewReq, "processing completed")
}

func handleAdmission(req *admissionv1.AdmissionRequest) {
	if req == nil {
		return
	}

	// Only mutate Pods on CREATE (maybe update too?)
	if req.Kind.Kind != "Pod" || req.Operation != admissionv1.Create {
		return
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return
	}

	log.Printf("caught pod %s in namespace %s with images", pod.Name, pod.Namespace)
	allContainers := make([]*corev1.Container, 0)
	if len(pod.Spec.Containers) > 0 {
		for _, c := range pod.Spec.Containers {
			allContainers = append(allContainers, &c)
		}
	}
	if len(pod.Spec.InitContainers) > 0 {
		for _, c := range pod.Spec.InitContainers {
			allContainers = append(allContainers, &c)
		}
	}

	mutex.Lock()
	defer mutex.Unlock()

	for _, c := range allContainers {
		key := c.Image
		if _, exists := imagesMap[key]; !exists {
			imagesMap[key] = make([]string, 0)
			log.Printf("+++ New image found: %s", key)
		}
		imagesMap[key] = append(imagesMap[key], fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, c.Name))
		log.Printf("*** Pod %s/%s container %s with image %s", pod.Name, pod.Namespace, c.Name, c.Image)
	}

	updatingImageCache()
}

// under protection of a mutex
func updatingImageCache() {
	if kubeClient == nil {
		log.Printf("Kubernetes client is not initialized, skipping image cache update")
		return
	}

	key := client.ObjectKey{
		Name:      "kubevirt-image-cache",
		Namespace: "kube-fledged",
	}
	imageCache := &fledged.ImageCache{}

	if err := kubeClient.Get(context.TODO(), key, imageCache); err != nil {
		if errors.IsNotFound(err) {
			imageCache = &fledged.ImageCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubevirt-image-cache",
					Namespace: "kube-fledged",
				},
				Spec: fledged.ImageCacheSpec{
					CacheSpec: []fledged.CacheSpecImages{
						{
							Images: []string{},
						},
					},
				},
			}
		} else {
			log.Printf("failed to get ImageCache: %v", err)
			return
		}
	}

	for image := range imagesMap {
		imageCache.Spec.CacheSpec[0].Images = append(imageCache.Spec.CacheSpec[0].Images, image)
	}
	if err := kubeClient.Update(context.TODO(), imageCache); err != nil {
		log.Printf("failed to update ImageCache: %v", err)
	} else {
		log.Printf("updated ImageCache with %d images", len(imageCache.Spec.CacheSpec[0].Images))
	}

}
