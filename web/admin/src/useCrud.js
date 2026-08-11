import { ref, onMounted } from 'vue'

/**
 * 通用 CRUD 组合式函数，统一处理：列表加载、表单编辑、保存、删除、错误提示。
 * @param {{list:Function,save:Function,remove:Function}} api 资源客户端
 * @param {Function} emptyForm 返回一个空表单对象的工厂函数
 */
export function useCrud(api, emptyForm) {
  const rows = ref([])
  const form = ref(emptyForm())
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')
  const message = ref('')

  function resetForm() {
    form.value = emptyForm()
  }

  function notify(msg) {
    message.value = msg
    setTimeout(() => (message.value = ''), 2000)
  }

  async function load(query) {
    loading.value = true
    error.value = ''
    try {
      rows.value = (await api.list(query)) || []
    } catch (e) {
      error.value = e.message
    } finally {
      loading.value = false
    }
  }

  async function save(query) {
    saving.value = true
    error.value = ''
    try {
      await api.save(form.value)
      resetForm()
      await load(query)
      notify('保存成功')
    } catch (e) {
      error.value = e.message
    } finally {
      saving.value = false
    }
  }

  function edit(row) {
    form.value = { ...row }
  }

  async function remove(row, query) {
    if (!confirm('确认删除该配置？')) return
    error.value = ''
    try {
      await api.remove(row.id)
      await load(query)
      notify('删除成功')
    } catch (e) {
      error.value = e.message
    }
  }

  onMounted(() => load())

  return {
    rows,
    form,
    loading,
    saving,
    error,
    message,
    load,
    save,
    edit,
    remove,
    resetForm
  }
}
