# ESXi HTTP Deploy (UEFI HTTP BOOT)

ESXi HTTP Deploy is a simple utility to extract the contents of ESXi install iso media and make the nessesary modifications to enable UEFI HTTP boot on hardware. It will run its own web server on specified port. 

Works both on linux and windows.

## Command
This will extract the -iso to http/default_media/{name} and host it on port 9191 with default kickstart options. Name is set on -name paremeter.

`./ESXI_HTTP_Deploy -iso "/home/vmadmin/iso/VMware-VMvisor-Installer-8.0U1a-21813344.x86_64.iso" -port 9191 -name 8.0U1a-21813344 -ks`

if you dont want the default kickstart script, then exclude -ks. This will then be a default manual installation.
