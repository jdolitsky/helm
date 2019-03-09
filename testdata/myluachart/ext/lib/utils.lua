-- utils.lua

-- Returns a Pod resource that just runs a command in busybox image
local function simple_exec(command, suffix)
    return {
        apiVersion = "v1",
        kind = "Pod",
        metadata = {
            name = release.name .. "-" .. chart.name .. "-" .. suffix
        },
        spec = {
            containers = {
                {
                    image = "busybox",
                    name = chart.name,
                    command = {"/bin/sh", "-c", command}
                }
            }
        }
    }
end

return {
    simple_exec = simple_exec
}