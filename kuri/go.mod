module github.com/aleksclark/crush-modules/kuri

go 1.26.3

require (
	charm.land/fantasy v0.29.0
	github.com/charmbracelet/crush v0.0.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/charmbracelet/x/exp/slice v0.0.0-20260422141420-a6cbdff8a7e2 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/go-json-experiment/json v0.0.0-20260520185125-572e7c383686 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kaptinlin/go-i18n v0.4.9 // indirect
	github.com/kaptinlin/jsonpointer v0.4.25 // indirect
	github.com/kaptinlin/jsonschema v0.7.15 // indirect
	github.com/kaptinlin/messageformat-go v0.6.4 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/text v0.37.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/charmbracelet/crush => ../../crush-plugin-poc

replace github.com/aleksclark/crush-modules => ../
