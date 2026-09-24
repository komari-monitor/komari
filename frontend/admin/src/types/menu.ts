export interface MenuItem {
  labelKey: string;
  path: string;
  icon: string;
  children?: MenuItem[];
  newTab?: boolean;
  bottom?: boolean;
}
