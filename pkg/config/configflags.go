package config

const (
	TestFlag DynamicFlag = "ExampleFeature_ExampleFlag"
)

func RegisterFlags(dc *DynamicConfig) {
	dc.RegisterBool(TestFlag, false)
}
