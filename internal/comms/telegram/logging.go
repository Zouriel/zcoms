package telegram

func ConfigureLogging(tdjson *TDJSON) {
	_, _ = tdjson.Execute(`{"@type":"setLogVerbosityLevel","new_verbosity_level":0}`)
	_, _ = tdjson.Execute(`{"@type":"setLogStream","log_stream":{"@type":"logStreamEmpty"}}`)
}
