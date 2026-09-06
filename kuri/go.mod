module github.com/aleksclark/crush-modules/kuri

go 1.27.0

require (
	charm.land/fantasy v0.43.0
	github.com/charmbracelet/crush v0.0.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/charmbracelet/x/exp/slice v0.0.0-20260730164118-7e2d3e6c5238 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kaptinlin/jsonpointer v0.4.28 // indirect
	github.com/kaptinlin/jsonschema v0.9.9 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/charmbracelet/crush => ../../crush-plugin-poc

replace github.com/aleksclark/crush-modules => ../
