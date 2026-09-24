import { toast } from 'vue-sonner'

export interface MessageApi {
  success: (msg: string) => void
  error: (msg: string) => void
  warning: (msg: string) => void
  info: (msg: string) => void
}

export const message: MessageApi = {
  success: (msg: string) => {
    toast.success(msg)
  },
  error: (msg: string) => {
    toast.error(msg)
  },
  warning: (msg: string) => {
    toast.warning(msg)
  },
  info: (msg: string) => {
    toast.info(msg)
  },
}
