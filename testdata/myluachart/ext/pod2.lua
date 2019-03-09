-- pod2.lua

-- External libraries
local simple_exec = require("lib/utils").simple_exec

-- Add a simple pod to resources that prints the chart name every 10 seconds
local command = "while true; do echo "..chart.name.."; sleep 10; done"
local pod = simple_exec(command, "p2")
resources.add(pod)
