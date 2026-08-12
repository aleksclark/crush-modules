module github.com/aleksclark/crush-modules/kuri

go 1.26.5

require (
	charm.land/fantasy v0.41.0
	github.com/charmbracelet/crush v0.0.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/charmbracelet/x/exp/slice v0.0.0-20260730164118-7e2d3e6c5238 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kaptinlin/jsonpointer v0.4.28 // indirect
	github.com/kaptinlin/jsonschema v0.9.8 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/charmbracelet/crush => ../../crush-plugin-poc

replace github.com/aleksclark/crush-modules => ../
