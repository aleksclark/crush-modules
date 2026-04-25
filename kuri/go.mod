module github.com/aleksclark/crush-modules/kuri

go 1.26.2

require (
	charm.land/fantasy v0.20.0
	github.com/charmbracelet/crush v0.0.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/charmbracelet/x/exp/slice v0.0.0-20260209194814-eeb2896ac759 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/go-json-experiment/json v0.0.0-20260214004413-d219187c3433 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kaptinlin/go-i18n v0.3.0 // indirect
	github.com/kaptinlin/jsonpointer v0.4.17 // indirect
	github.com/kaptinlin/jsonschema v0.7.7 // indirect
	github.com/kaptinlin/messageformat-go v0.4.19 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/text v0.36.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/charmbracelet/crush => ../../crush-plugin-poc

replace github.com/aleksclark/crush-modules => ../
