-- pod1.lua

-- External libraries
local simple_exec = require("lib/utils").simple_exec

-- Add a simple pod to resources that prints the release name every 5 seconds
local command = "while true; do echo "..release.name.."; sleep 5; done"
local pod = simple_exec(command, "p1")
resources.add(pod)
