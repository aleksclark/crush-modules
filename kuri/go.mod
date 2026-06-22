module github.com/aleksclark/crush-modules/kuri

go 1.26.4

require (
	charm.land/fantasy v0.33.1
	github.com/charmbracelet/crush v0.0.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/charmbracelet/x/exp/slice v0.0.0-20260615092313-b57e5e6d29bb // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/go-json-experiment/json v0.0.0-20260601182631-00ed12fed2a6 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kaptinlin/jsonpointer v0.4.26 // indirect
	github.com/kaptinlin/jsonschema v0.8.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/charmbracelet/crush => ../../crush-plugin-poc

replace github.com/aleksclark/crush-modules => ../
