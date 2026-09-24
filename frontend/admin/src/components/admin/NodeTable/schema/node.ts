import { z } from "zod";

export interface ClientFormData {
  name: string;
  token: string;
  remark: string;
  public_remark: string;
}

export const schema = z.object({
  uuid: z.string(),
  name: z.string(),
  cpu_name: z.string().optional(),
  arch: z.string().optional(),
  cpu_cores: z.number().optional(),
  os: z.string().optional(),
  gpu_name: z.string().optional(),
  ipv4: z.string(),
  ipv6: z.string().optional(),
  region: z.string().optional(),
  mem_total: z.number().optional(),
  swap_total: z.number().optional(),
  disk_total: z.number().optional(),
  version: z.string(),
  weight: z.number().optional(),
  price: z.number().optional(),
  expired_at: z.string().optional(),
  created_at: z.string().optional(),
  updated_at: z.string().optional(),
  token: z.string().optional(),
  remark: z.string().optional(),
  public_remark: z.string().optional(),
});

export type NodeSchema = z.infer<typeof schema>;
