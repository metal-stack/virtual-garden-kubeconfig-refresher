package main

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"k8s.io/client-go/rest"
)

// taken from https://gist.github.com/ahmetb/548059cdbf12fb571e4e2f1e29c48997

var (
	googleScopes = []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email"}
)

const (
	googleAuthPlugin = "google" // so that this is different than "gcp" that's already in client-go tree.
)

func init() {
	if err := rest.RegisterAuthProviderPlugin(googleAuthPlugin, newGoogleAuthProvider); err != nil {
		panic(fmt.Errorf("failed to register %s auth plugin: %w", googleAuthPlugin, err))
	}
}

var _ rest.AuthProvider = &googleAuthProvider{}

type googleAuthProvider struct {
	tokenSource oauth2.TokenSource
}

func (g *googleAuthProvider) WrapTransport(rt http.RoundTripper) http.RoundTripper {
	return &oauth2.Transport{
		Base:   rt,
		Source: g.tokenSource,
	}
}
func (g *googleAuthProvider) Login() error { return nil }

func newGoogleAuthProvider(addr string, config map[string]string, persister rest.AuthProviderConfigPersister) (rest.AuthProvider, error) {
	ts, err := google.DefaultTokenSource(context.TODO(), googleScopes...)
	if err != nil {
		return nil, fmt.Errorf("failed to create google token source: %w", err)
	}
	return &googleAuthProvider{tokenSource: ts}, nil
}
