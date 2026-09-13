<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialMark } from '@/api/control/types'
import AppTooltip from '@/components/ui/AppTooltip.vue'

import { credentialMarkLabel, credentialMarkTone } from './credential-mark'

const props = defineProps<{ mark: CredentialMark; note: string; variant: 'badge' | 'dot' }>()

const { t } = useI18n()

const tone = computed(() => credentialMarkTone(props.mark))
const label = computed(() => credentialMarkLabel(t, props.mark, props.note))
const tooltip = computed(() => `${t('group.credentials.mark.title')} · ${label.value}`)
</script>

<template>
  <AppTooltip v-if="tone" :content="tooltip">
    <span
      class="credential-mark"
      :class="[`credential-mark--${variant}`, `credential-mark--${tone}`]"
      role="img"
      tabindex="0"
      :aria-label="tooltip"
      ><template v-if="variant === 'badge'">{{ label }}</template></span
    >
  </AppTooltip>
</template>

<style scoped>
/* 实心块比软色底更跳眼，和状态徽章明显区分开，一眼能看出是人工标记。 */
.credential-mark {
  flex: none;
  color: var(--color-text-inverse);
}
.credential-mark:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
/* 提高一级特异性，压过卡片徽章行的 `> * { max-width: 100% }`，
   让长备注在徽章里省略号截断，而不是撑宽或逐字换行。 */
.credential-mark.credential-mark--badge {
  display: inline-block;
  max-width: 18ch;
  border-radius: var(--radius-control);
  padding: 1px 7px;
  font-size: var(--text-label-xs);
  font-weight: 650;
  line-height: 1.6;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 圆点只占 8px，放在账号名左侧不会改变行高，也不会挤掉后面的内容。 */
.credential-mark.credential-mark--dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
.credential-mark--warning {
  background: var(--color-warning);
}
.credential-mark--danger {
  background: var(--color-danger);
}
.credential-mark--info {
  background: var(--color-info);
}
</style>
