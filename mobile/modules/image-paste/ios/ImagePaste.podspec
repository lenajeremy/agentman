Pod::Spec.new do |s|
  s.name           = 'ImagePaste'
  s.version        = '1.0.0'
  s.summary        = 'Lets Paste put an image into a React Native text input'
  s.description    = 'Lets the system Paste command put an image into a React Native text input'
  s.license        = 'MIT'
  s.author         = 'Agentman'
  s.homepage       = 'https://github.com/lenajeremy/agentman'
  s.platforms      = { :ios => '15.1' }
  s.swift_version  = '5.9'
  s.source         = { git: 'https://github.com/lenajeremy/agentman.git' }
  s.static_framework = true

  s.dependency 'ExpoModulesCore'

  s.source_files = "**/*.{h,m,swift}"
  s.pod_target_xcconfig = {
    'DEFINES_MODULE' => 'YES',
    'SWIFT_COMPILATION_MODE' => 'wholemodule'
  }
end
