import { createApp, h, nextTick, reactive } from 'vue'
import { afterEach, describe, expect, it } from 'vitest'
import { t } from '@/i18n'
import ConfirmDialog from './ConfirmDialog.vue'

type Props = InstanceType<typeof ConfirmDialog>['$props']

let cleanup: (() => void) | null = null

// The dialog stays closed: jsdom has no modal support, and the form is rendered either way.
function mount(initial: Partial<Props>) {
  const props = reactive<Record<string, unknown>>({ open: false, title: 'Title', ...initial })
  const events: string[] = []
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () =>
      h(ConfirmDialog, {
        ...(props as unknown as Props),
        onConfirm: () => events.push('confirm'),
        onCancel: () => events.push('cancel'),
      }),
  })
  app.config.globalProperties.$t = t
  app.mount(host)
  cleanup = () => {
    app.unmount()
    host.remove()
  }
  const submit = () => host.querySelector<HTMLButtonElement>('button[type=submit]') as HTMLButtonElement
  const form = () => host.querySelector('form') as HTMLFormElement
  return { props, events, host, submit, form }
}

afterEach(() => {
  cleanup?.()
  cleanup = null
})

describe('ConfirmDialog', () => {
  it('can be confirmed when the parent passes no extra condition', async () => {
    const d = mount({})
    expect(d.submit().disabled).toBe(false)
    d.form().dispatchEvent(new Event('submit', { cancelable: true }))
    await nextTick()
    expect(d.events).toEqual(['confirm'])
  })

  it('follows canConfirm and busy', async () => {
    const d = mount({ canConfirm: false })
    expect(d.submit().disabled).toBe(true)
    d.form().dispatchEvent(new Event('submit', { cancelable: true }))
    expect(d.events).toEqual([])
    d.props.canConfirm = true
    await nextTick()
    expect(d.submit().disabled).toBe(false)
    d.props.busy = true
    await nextTick()
    expect(d.submit().disabled).toBe(true)
  })

  it('waits for the expected text', async () => {
    const d = mount({ expectText: 'alice', expectLabel: 'Type alice' })
    expect(d.submit().disabled).toBe(true)
    const input = d.host.querySelector('input') as HTMLInputElement
    input.value = 'alice'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    expect(d.submit().disabled).toBe(false)
  })
})
