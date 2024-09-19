package transport

import "net/http"

type RoundTripper interface {
	RoundTripper(*http.Request) (*http.Response, error)
	DetailedRoundTrip(*http.Request) (*RoundTripDetails, *http.Response, error)
}
