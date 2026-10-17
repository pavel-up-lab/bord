// Package media validates, normalizes, stores, and reconciles untrusted images.
//
// The package deliberately supports only JPEG and PNG. Every accepted image is
// decoded and encoded again before it can enter the blob store, so optional
// source metadata is not retained.
package media
