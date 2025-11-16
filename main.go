package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	certFile string
	keyFile  string
	port     int
)

// imageUrl : podName/Namespace/containerName
var imagesMap = make(map[string][]string)

func init() {
	flag.StringVar(&certFile, "tls-cert", "/etc/webhooks/cdi-images-validator-certs/tls.crt", "TLS certificate file")
	flag.StringVar(&keyFile, "tls-key", "/etc/webhooks/cdi-images-validator-certs/tls.key", "TLS key file")
	flag.IntVar(&port, "port", 8443, "Webhook server port")
	flag.Parse()
}

func main() {
	http.HandleFunc("/validate-images", admitHandler)
	http.HandleFunc("/dump-images", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(imagesMap); err != nil {
			http.Error(w, "failed to encode images map", http.StatusInternalServerError)
			return
		}
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
	err := http.ListenAndServeTLS(addr, certFile, keyFile, nil)
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

func admitHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var reviewReq admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &reviewReq); err != nil {
		http.Error(w, "failed to unmarshal admission review", http.StatusBadRequest)
		return
	}

	resp := handleAdmission(&reviewReq)

	// admission.k8s.io/v1, Kind=AdmissionReview, got /, Kind="

	reviewResp := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: resp,
	}
	// must copy UID from request to response
	if reviewReq.Request != nil {
		reviewResp.Response.UID = reviewReq.Request.UID
	}

	respBody, err := json.Marshal(reviewResp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBody)
}

func handleAdmission(ar *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {

	req := ar.Request
	if req == nil {
		return toAdmissionResponseError("empty request")
	}

	// Only mutate Pods on CREATE
	if req.Kind.Kind != "Pod" || req.Operation != admissionv1.Create {
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return toAdmissionResponseError("could not decode pod object: " + err.Error())
	}

	// log.Printf("caught pod %s in namespace %s with images", pod.Name, pod.Namespace)
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

	for _, c := range allContainers {
		key := c.Image
		if _, exists := imagesMap[key]; !exists {
			imagesMap[key] = make([]string, 0)
		}
		imagesMap[key] = append(imagesMap[key], fmt.Sprintf("%s/%s/%s", pod.Namespace, pod.Name, c.Name))
		log.Printf("Pod %s/%s container %s with image %s", pod.Name, pod.Namespace, c.Name, c.Image)
	}
	return &admissionv1.AdmissionResponse{
		Allowed: true,
	}
}

func toAdmissionResponseError(message string) *admissionv1.AdmissionResponse {
	log.Println(message)
	return &admissionv1.AdmissionResponse{
		Allowed: false,
		Result:  nil,
	}
}
