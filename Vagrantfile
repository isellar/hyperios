# HyperiOS dev environment
# Ubuntu 24.04 LTS VM with headless sway, cloud-init provisioned
#
# Usage:
#   vagrant up          # create and provision
#   vagrant ssh         # shell into VM
#   vagrant provision   # re-run provisioner without recreating
#   vagrant destroy -f  # tear down cleanly
#
# Prerequisites: VirtualBox + Vagrant installed on host

Vagrant.configure("2") do |config|
  # ubuntu/noble64 is no longer maintained on Vagrant Cloud; bento is the recommended alternative
  config.vm.box = "bento/ubuntu-24.04"
  config.vm.hostname = "hyperios-dev"

  config.vm.provider "virtualbox" do |vb|
    vb.name   = "hyperios-dev"
    vb.memory = 2048
    vb.cpus   = 2
    # Enable PAE/NX and nested paging for better performance
    vb.customize ["modifyvm", :id, "--pae", "on"]
    vb.customize ["modifyvm", :id, "--nested-hw-virt", "on"]
  end

  # Sync repo root into VM — binary and configs available at /vagrant
  config.vm.synced_folder ".", "/vagrant", type: "virtualbox"

  # Port forwards for future use (web UI, etc.)
  config.vm.network "forwarded_port", guest: 8080, host: 8080, auto_correct: true

  # Run provisioner
  config.vm.provision "shell", path: "distro/dev/provision.sh"
end
