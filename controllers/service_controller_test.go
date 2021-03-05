package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetHolepunchPortMapping(t *testing.T) {
	portMapping, err := getHolepunchPortMapping(corev1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:        "my-service",
			Namespace:   "default",
			Annotations: map[string]string{
				holepunchAnnotationName: "true",
				holepunchPortMapAnnotationPrefix + "80": "3000",
				holepunchPortMapAnnotationPrefix + "443": "4000",
			},
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, portMapping, map[uint16]uint16{
		80: 3000,
		443: 4000,
	})
}

func TestGetHolepunchPortMappingNonNumericErrors(t *testing.T) {
	portMapping, err := getHolepunchPortMapping(corev1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:        "my-service",
			Namespace:   "default",
			Annotations: map[string]string{
				holepunchAnnotationName: "true",
				holepunchPortMapAnnotationPrefix + "80": "some-non-numeric-value",
			},
		},
	})
	assert.Error(t, err)
	assert.Nil(t, portMapping)
}

func TestGetHolepunchPortMappingInvalidPortNumberErrors(t *testing.T) {
	portMapping, err := getHolepunchPortMapping(corev1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:        "my-service",
			Namespace:   "default",
			Annotations: map[string]string{
				holepunchAnnotationName: "true",
				// 70,000 is too high for a port number (on Linux)
				holepunchPortMapAnnotationPrefix + "80": "70000",
			},
		},
	})
	assert.Error(t, err)
	assert.Nil(t, portMapping)
}