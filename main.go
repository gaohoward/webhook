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
)

var (
	certFile string
	keyFile  string
	port     int
)

func init() {
	flag.StringVar(&certFile, "tls-cert", "/etc/webhooks/cdi-images-validator-certs/tls.crt", "TLS certificate file")
	flag.StringVar(&keyFile, "tls-key", "/etc/webhooks/cdi-images-validator-certs/tls.key", "TLS key file")
	flag.IntVar(&port, "port", 8443, "Webhook server port")
	flag.Parse()
}

func main() {
	http.HandleFunc("/validate-images", admitHandler)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	http.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("starting mutating webhook server on %s", addr)
	err := http.ListenAndServeTLS(addr, certFile, keyFile, nil)
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

// type mutatingImageFunc func(v1.AdmissionReview) *v1.AdmissionResponse {

// }

func admitHandler(w http.ResponseWriter, r *http.Request) {

	log.Println("---in http validate handler...")
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

	reviewResp := admissionv1.AdmissionReview{
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
	log.Printf("---in handle admission kind: %v and op: %v", req.Kind.Kind, req.Operation)

	// Only mutate Pods on CREATE
	if req.Kind.Kind != "Pod" || req.Operation != admissionv1.Create {
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	log.Println("get pod")

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return toAdmissionResponseError("could not decode pod object: " + err.Error())
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
