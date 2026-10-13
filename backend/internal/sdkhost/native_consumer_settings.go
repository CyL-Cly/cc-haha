package sdkhost

func nativePlanSettingsBound(options Options) bool {
	return options.Print != nil && options.Print.Tools != nil && options.Print.Tools.HasPlanBindings() && options.Print.PlanProjection != nil && options.Print.PlanAttachments != nil
}
