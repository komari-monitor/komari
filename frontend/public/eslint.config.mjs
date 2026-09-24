import antfu from '@antfu/eslint-config'

export default antfu({
  ignores: ['public/admin-app/**'],
  formatters: true,
  vue: true,
})
